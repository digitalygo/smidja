package managerstate

import "testing"

func TestSessionIdentityDurableAndImmutable(t *testing.T) {
	s, dir := openTestStore(t)
	session, _ := s.CreateSession("team")
	for _, args := range [][3]string{{"", "ref", "id"}, {"chat", "", "id"}, {"chat", "ref", ""}} {
		if _, err := s.AttachSessionIdentity(session.ID, args[0], args[1], args[2]); err == nil {
			t.Fatal(args)
		}
	}
	if _, err := s.AttachSessionIdentity("missing", "chat", "ref", "id"); err == nil {
		t.Fatal("missing session")
	}
	if _, err := s.AttachSessionIdentity(session.ID, "chat", "ref", "id"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachSessionIdentity(session.ID, "chat", "ref", "id"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][3]string{{"other", "ref", "id"}, {"chat", "other", "id"}, {"chat", "ref", "other"}} {
		if _, err := s.AttachSessionIdentity(session.ID, args[0], args[1], args[2]); err == nil {
			t.Fatal(args)
		}
	}
	second, _ := s.CreateSession("second")
	if _, err := s.AttachSessionIdentity(second.ID, "chat", "different", "id"); err == nil {
		t.Fatal("conflicting chat identity")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, _ := restored.GetSession(session.ID)
	if got.TeamRef != "ref" || got.TeamID != "id" || got.ChatID != "chat" {
		t.Fatal(got)
	}
}

func TestRejectedSessionAdmissionIsDurable(t *testing.T) {
	s, dir := openTestStore(t)
	session, err := s.CreateSession("rejected")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RejectSessionAdmission("missing"); err == nil {
		t.Fatal("missing session accepted")
	}
	if _, err = s.RejectSessionAdmission(session.ID); err == nil {
		t.Fatal("unattached session accepted")
	}
	if _, err = s.AttachSessionIdentity(session.ID, "chat", "team", "team"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if session, err = s.RejectSessionAdmission(session.ID); err != nil || !session.AdmissionRejected {
			t.Fatal(session, err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetSession(session.ID)
	if err != nil || !got.AdmissionRejected || got.ChatID != "chat" || got.TeamID != "team" {
		t.Fatal(got, err)
	}
}

func TestActiveSessionAdmissionCannotBeRejected(t *testing.T) {
	s, _ := openTestStore(t)
	session, _ := s.CreateSession("active")
	if _, err := s.AttachSessionIdentity(session.ID, "chat", "team", "team"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartSessionTurn(session.ID, "request", "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordChatSequence(session.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RejectSessionAdmission(session.ID); err == nil {
		t.Fatal("active session rejected")
	}
}

func TestRejectedSessionAdmissionPreservesPreTurnCursor(t *testing.T) {
	s, dir := openTestStore(t)
	session, _ := s.CreateSession("pre-turn notice")
	if _, err := s.AttachSessionIdentity(session.ID, "chat", "team", "team"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordChatSequence(session.ID, 1); err != nil {
		t.Fatal(err)
	}
	rejected, err := s.RejectSessionAdmission(session.ID)
	if err != nil || !rejected.AdmissionRejected || rejected.Turn != nil || rejected.LastSequence != 1 {
		t.Fatal(rejected, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetSession(session.ID)
	if err != nil || !got.AdmissionRejected || got.Turn != nil || got.LastSequence != 1 {
		t.Fatal(got, err)
	}
}
