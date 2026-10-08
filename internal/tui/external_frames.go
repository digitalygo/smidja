package tui

import "reflect"

type ExternalFramePreparer interface {
	PrepareExternalFrame(width int)
}

func SameComponent(first, second Component) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	firstValue := reflect.ValueOf(first)
	secondValue := reflect.ValueOf(second)
	if firstValue.Type() != secondValue.Type() {
		return false
	}
	if firstValue.Type().Comparable() {
		return first == second
	}
	return false
}
