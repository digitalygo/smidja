package tui

import "errors"

var errImageOpenUnsupported = errors.New("tui: anchored image opening is unsupported on this platform")

type imageOpenStage int

const (
	imageOpenStageRoot imageOpenStage = iota
	imageOpenStageComponent
	imageOpenStageTraversed
	imageOpenStageValidated
	imageOpenStageHeld
)

type imageOpenHook func(stage imageOpenStage, path string) error

func runImageOpenHook(hook imageOpenHook, stage imageOpenStage, path string) error {
	if hook == nil {
		return nil
	}
	return hook(stage, path)
}
