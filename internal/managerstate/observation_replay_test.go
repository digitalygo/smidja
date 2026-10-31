package managerstate

import "testing"

func TestObservationResumePreservesLegacyJournal(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		s, dir := openTestStore(t)
		task := startTestTask(t, s)
		if _, err := s.StartPhase(task.ID, "original"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pause(task.ID, "offline"); err != nil {
			t.Fatal(err)
		}
		if legacy {
			if _, err := s.change(event{Type: "task.resumed", TaskID: task.ID, Text: "historical retry"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.StartPhase(task.ID, "historical-new-attempt"); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := s.Resume(task.ID, "recover observer"); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		got, err := reopened.GetTask(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if legacy {
			want = 2
		}
		if got.Status != Running || len(got.Phases[0].Attempts) != want {
			t.Fatal(got)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
