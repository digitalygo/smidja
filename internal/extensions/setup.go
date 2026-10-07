package extensions

type setupTransaction interface {
	BeginSetup() func(failed bool)
}

func (a *api) BeginSetup() func(failed bool) {
	if a == nil {
		return func(bool) {}
	}
	flags := a.flags.snapshot()
	providers := a.providers.snapshot()
	events := a.events.snapshot()
	tools := a.catalog.snapshot()
	commands := a.commands.snapshot()
	ui := a.ui.Snapshot()
	return func(failed bool) {
		if !failed {
			return
		}
		a.flags.restore(flags)
		a.providers.restore(providers)
		a.events.restore(events)
		a.catalog.restore(tools)
		a.commands.restore(commands)
		a.ui.Restore(ui)
	}
}
