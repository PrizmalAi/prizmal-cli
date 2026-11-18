package opencode

// majorCapabilities is what one OpenCode major supports, consulted instead
// of scattering `major == openCodeMajor2` checks through the launch code.
// The zero value is the 1.x row: every field here is something 2.x gains or
// 2.x loses relative to 1.x, never the other way around.
type majorCapabilities struct {
	// effectAuthPlugin selects the 2.x `Plugin.define({ id, setup })`
	// device-auth plugin, registering `session.model.request`. False
	// selects the 1.x `chat.headers` plugin.
	effectAuthPlugin bool

	// dropsReasoningFields is set for a major that silently drops a model
	// entry's reasoning, temperature and attachment fields (OpenCode 2.x's
	// config migration logs a warning and keeps going). buildModelEntries
	// does not send those fields to a major with this set.
	dropsReasoningFields bool

	// searchNeedsUnprovenToolTransform is set for a major whose search route
	// is ctx.tool.transform and whose input-schema helper the ticket's
	// live-run audit left unverified. 1.x's tools/ directory tool is unconditional and
	// never sets this; a major that does set it degrades search instead of
	// registering a tool this launcher has not proven against a live run, so
	// 2.x never ships a broken one.
	searchNeedsUnprovenToolTransform bool
}

// capabilitiesByMajor is the one capability table, keyed by major. A major
// absent from it (openCodeMajorUnknown, or any future major this launcher
// does not yet know) falls back to the 1.x row in capabilitiesFor, matching
// resolveOpenCodeMajor's own fail-open rule.
var capabilitiesByMajor = map[openCodeMajor]majorCapabilities{
	openCodeMajor1: {},
	openCodeMajor2: {
		effectAuthPlugin:                 true,
		dropsReasoningFields:             true,
		searchNeedsUnprovenToolTransform: true,
	},
}

// capabilitiesFor returns major's row, or the 1.x row for a major the table
// does not carry.
func capabilitiesFor(major openCodeMajor) majorCapabilities {
	if c, ok := capabilitiesByMajor[major]; ok {
		return c
	}
	return capabilitiesByMajor[openCodeMajor1]
}
