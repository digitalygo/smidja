package sdk

type CustomEvent struct {
	Name string

	Data any
}

type CustomEventHandler func(event CustomEvent) error

type CustomEventSubscription interface {
	SubscribeCustomEvent(name string, handler CustomEventHandler) (unsubscribe func(), err error)
}
