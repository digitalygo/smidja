package verktriggers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var ErrSignature = errors.New("invalid webhook signature")
var ErrUnsupported = errors.New("unsupported webhook event")
var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,255}$`)
var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Event struct {
	Type        string          `json:"type"`
	Source      string          `json:"source"`
	DeliveryID  string          `json:"delivery_id"`
	Repository  string          `json:"repository,omitempty"`
	Branch      string          `json:"branch,omitempty"`
	Action      string          `json:"action,omitempty"`
	SHA         string          `json:"sha,omitempty"`
	PayloadHash string          `json:"payload_hash"`
	Payload     json.RawMessage `json:"payload"`
}

func Authenticate(headers http.Header, body []byte, secret string) (string, string, error) {
	source, delivery := headers.Get("X-Smidja-Source"), headers.Get("X-Smidja-Delivery")
	signature := headers.Get("X-Smidja-Signature-256")
	message := body
	if headers.Get("X-GitHub-Event") != "" {
		if source != "" || delivery != "" || signature != "" {
			return "", "", ErrSignature
		}
		source = "github"
		delivery = headers.Get("X-GitHub-Delivery")
		signature = headers.Get("X-Hub-Signature-256")
	} else {
		if source == "github" || strings.HasPrefix(source, "github:") {
			return "", "", ErrSignature
		}
		message = append([]byte(source+"\n"+delivery+"\n"), body...)
	}
	if secret == "" || !safeID.MatchString(source) || !safeID.MatchString(delivery) || !strings.HasPrefix(signature, "sha256=") {
		return "", "", ErrSignature
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(decoded) != sha256.Size {
		return "", "", ErrSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(message)
	if !hmac.Equal(decoded, mac.Sum(nil)) {
		return "", "", ErrSignature
	}
	return source, delivery, nil
}

func Decode(headers http.Header, body []byte, source, delivery string) (Event, error) {
	digest := sha256.Sum256(body)
	event := Event{Type: "webhook", Source: source, DeliveryID: delivery, PayloadHash: hex.EncodeToString(digest[:]), Payload: append(json.RawMessage(nil), body...)}
	var payload struct {
		Action     string `json:"action"`
		Ref        string `json:"ref"`
		Before     string `json:"before"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
		PullRequest *struct {
			Number int `json:"number"`
			Head   struct {
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		} `json:"pull_request"`
		WorkflowRun *ciPayload `json:"workflow_run"`
		CheckRun    *ciPayload `json:"check_run"`
		CheckSuite  *ciPayload `json:"check_suite"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return Event{}, fmt.Errorf("invalid webhook JSON")
	}
	if decoder.Decode(new(any)) != io.EOF || object == nil {
		return Event{}, fmt.Errorf("webhook must contain one JSON object")
	}
	if source != "github" {
		return event, nil
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Event{}, fmt.Errorf("invalid GitHub payload")
	}
	if !repositoryName.MatchString(payload.Repository.FullName) {
		return Event{}, fmt.Errorf("missing signed repository identity")
	}
	event.Repository = strings.ToLower(payload.Repository.FullName)
	if owner := payload.Repository.Owner.Login; owner != "" && !strings.EqualFold(owner, strings.Split(event.Repository, "/")[0]) {
		return Event{}, fmt.Errorf("repository owner mismatch")
	}
	event.Action = payload.Action
	shapes := 0
	for _, present := range []bool{payload.Ref != "", payload.PullRequest != nil, payload.WorkflowRun != nil, payload.CheckRun != nil, payload.CheckSuite != nil} {
		if present {
			shapes++
		}
	}
	if shapes != 1 {
		return Event{}, fmt.Errorf("ambiguous webhook event shape")
	}
	switch headers.Get("X-GitHub-Event") {
	case "push":
		if payload.Ref == "" || payload.Before == "" || payload.After == "" || payload.Action != "" {
			return Event{}, fmt.Errorf("invalid push payload")
		}
		if payload.Deleted || !strings.HasPrefix(payload.Ref, "refs/heads/") {
			return Event{}, ErrUnsupported
		}
		event.Type = "push"
		event.Branch = strings.TrimPrefix(payload.Ref, "refs/heads/")
		event.SHA = payload.After
		event.Action = "push"
	case "pull_request":
		if payload.PullRequest == nil || payload.PullRequest.Number < 1 || payload.Action == "" || payload.PullRequest.Base.Ref == "" || payload.PullRequest.Head.SHA == "" {
			return Event{}, fmt.Errorf("invalid pull request payload")
		}
		event.Type = "pr"
		event.Branch = payload.PullRequest.Base.Ref
		event.SHA = payload.PullRequest.Head.SHA
	case "workflow_run", "check_run", "check_suite":
		run := payload.WorkflowRun
		if headers.Get("X-GitHub-Event") == "check_run" {
			run = payload.CheckRun
		}
		if headers.Get("X-GitHub-Event") == "check_suite" {
			run = payload.CheckSuite
		}
		if run == nil || run.ID < 1 {
			return Event{}, fmt.Errorf("invalid CI payload")
		}
		if payload.Action != "completed" || run.Status != "completed" || run.Conclusion == "" {
			return Event{}, ErrUnsupported
		}
		event.Type = "ci"
		event.Action = run.Conclusion
		event.Branch = run.HeadBranch
		event.SHA = run.HeadSHA
		if event.Branch == "" && run.CheckSuite != nil {
			event.Branch = run.CheckSuite.HeadBranch
		}
	default:
		return Event{}, ErrUnsupported
	}
	return event, nil
}

type ciPayload struct {
	ID         int64  `json:"id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	CheckSuite *struct {
		HeadBranch string `json:"head_branch"`
	} `json:"check_suite"`
}
