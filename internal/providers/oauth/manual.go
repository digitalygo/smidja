package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func parseAuthorizationInput(input string) (code string, state string, ok bool) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", "", false
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Query().Get("code"), parsed.Query().Get("state"), true
	}
	if idx := strings.Index(value, "#"); idx >= 0 {
		return value[:idx], value[idx+1:], true
	}
	if strings.Contains(value, "code=") {
		if params, err := url.ParseQuery(value); err == nil {
			return params.Get("code"), params.Get("state"), true
		}
	}
	return value, "", true
}

func awaitCode(ctx context.Context, opts Options, prompt string, server *callbackServer) (callbackOutcome, error) {
	manualCtx, manualCancel := context.WithCancel(ctx)
	defer manualCancel()
	var manualInput string
	var manualErr error
	manualDone := make(chan struct{})
	manualStarted := opts.ManualCode != nil
	if manualStarted {
		go func() {
			defer close(manualDone)
			input, err := opts.ManualCode(manualCtx, prompt)
			manualInput = input
			manualErr = err
			if server != nil {
				server.cancel()
			}
		}()
	}
	if server == nil {
		if !manualStarted {
			<-ctx.Done()
			return callbackOutcome{}, ctxErrMessage(ctx.Err())
		}
		<-manualDone
		if manualErr != nil {
			return callbackOutcome{}, manualErr
		}
		return callbackOutcome{manualInput: manualInput}, nil
	}
	outcome, err := server.wait(ctx)
	if err != nil {
		return outcome, err
	}
	if outcome.err != nil || outcome.hasEntry || outcome.code != "" {
		return outcome, nil
	}
	if !manualStarted {
		return outcome, errors.New("missing authorization code")
	}
	<-manualDone
	if manualErr != nil {
		return callbackOutcome{}, manualErr
	}
	outcome.manualInput = manualInput
	return outcome, nil
}

func ctxErrMessage(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("login timed out: %w", err)
	}
	return fmt.Errorf("login cancelled: %w", err)
}

func notifyDeviceCode(opts Options, device DeviceCode) {
	if opts.DeviceCode != nil {
		opts.DeviceCode(device)
		return
	}
	if opts.OpenBrowser != nil {
		_ = opts.OpenBrowser(device.VerificationURI)
	}
}
