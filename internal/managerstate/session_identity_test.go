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
