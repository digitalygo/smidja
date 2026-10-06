package interactive

import "time"

func (s *StatusIndicator) SetVisible(visible bool) {
	s.mu.Lock()
	s.visible = visible
	s.mu.Unlock()
}

func (s *StatusIndicator) Visible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.visible
}

func (s *StatusIndicator) SetIndicator(frames []string, interval time.Duration) {
	s.mu.Lock()
	if frames == nil {
		s.frames = append([]string(nil), defaultSpinnerFrames...)
		s.framesSet = false
	} else {
		s.frames = sanitizeFrames(frames)
		s.framesSet = true
	}
	if interval > 0 {
		s.interval = interval
	} else {
		s.interval = defaultSpinnerInterval
	}
	s.frame = 0
	s.mu.Unlock()
}

func (s *StatusIndicator) Indicator() ([]string, time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.frames...), s.interval, s.framesSet
}
