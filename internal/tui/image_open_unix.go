//go:build linux || darwin

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func anchoredImageOpen(workspaceRoot, relative string, override func(string) (*os.File, error), hook imageOpenHook) (*os.File, error) {
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return nil, ErrImageUnsafePath
	}
	defer root.Close()
	if err := runImageOpenHook(hook, imageOpenStageRoot, workspaceRoot); err != nil {
		return nil, err
	}
	components := strings.Split(relative, string(filepath.Separator))
	directory := root
	heldDirectories := make([]*os.Root, 0, len(components))
	defer func() {
		for _, held := range heldDirectories {
			held.Close()
		}
	}()
	for index, component := range components {
		prefix := filepath.Join(components[:index+1]...)
		absolute := filepath.Join(workspaceRoot, prefix)
		info, err := directory.Lstat(component)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrImageNotRegular
		}
		if index == len(components)-1 {
			return openValidatedImage(directory, component, absolute, info, override, hook)
		}
		if !info.IsDir() {
			return nil, ErrImageNotRegular
		}
		if err := runImageOpenHook(hook, imageOpenStageComponent, absolute); err != nil {
			return nil, err
		}
		next, err := directory.OpenRoot(component)
		if err != nil {
			return nil, ErrImageNotRegular
		}
		heldDirectories = append(heldDirectories, next)
		nextInfo, err := next.Stat(".")
		if err != nil || !nextInfo.IsDir() || !os.SameFile(info, nextInfo) {
			return nil, ErrImageNotRegular
		}
		directory = next
		if err := runImageOpenHook(hook, imageOpenStageTraversed, absolute); err != nil {
			return nil, err
		}
	}
	return nil, ErrImageNotRegular
}

func openValidatedImage(directory *os.Root, component, absolute string, info os.FileInfo, override func(string) (*os.File, error), hook imageOpenHook) (*os.File, error) {
	if !info.Mode().IsRegular() {
		return nil, ErrImageNotRegular
	}
	if err := runImageOpenHook(hook, imageOpenStageValidated, absolute); err != nil {
		return nil, err
	}
	file, err := openImageDescriptor(directory, component, absolute, override)
	if err != nil {
		return nil, err
	}
	if err := runImageOpenHook(hook, imageOpenStageHeld, absolute); err != nil {
		file.Close()
		return nil, err
	}
	current, err := directory.Lstat(component)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		file.Close()
		return nil, ErrImageNotRegular
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(opened, current) {
		file.Close()
		return nil, ErrImageNotRegular
	}
	return file, nil
}

func openImageDescriptor(directory *os.Root, component, absolute string, override func(string) (*os.File, error)) (*os.File, error) {
	if override != nil {
		return override(absolute)
	}
	file, err := directory.OpenFile(component, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrImageNotRegular
	}
	return file, nil
}
