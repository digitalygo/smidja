// Package extensions implements the smidja phase 1 extension runtime:
// the extension registry, the hook handler collection, and the dispatcher
// that runs the phase 1 hook chains over the collected handlers. It is
// the internal face of the public sdk contract: it imports sdk for the
// extension interfaces and event shapes, and it implements the agent
// ports (internal/agent.HookDispatcher) that the loop and the CLI call at
// the corresponding points.
//
// The wiring model matches Pi's extension runner: extensions register in
// load order and their hooks run in extension registration order, then in
// the order each extension registered them. Every handler invocation is
// guarded: a returned error or a recovered panic is logged once (with the
// extension id and the event name) and the handler's outcome is skipped,
// so one failing extension never breaks the chain for the rest.
package extensions
