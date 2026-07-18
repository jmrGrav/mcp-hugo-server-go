package tools

// KnownScopes lists every scope this server may issue or enforce.
var KnownScopes = []string{"read", "write"}

type ToolDef struct {
	Name          string
	Description   string
	RequiredScope string
	InputSchema   any
}

type Registry struct {
	defs   []ToolDef
	byName map[string]ToolDef
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]ToolDef)}
}

func (r *Registry) Register(def ToolDef) {
	r.defs = append(r.defs, def)
	r.byName[def.Name] = def
}

// All returns every registered ToolDef in registration order.
func (r *Registry) All() []ToolDef {
	return r.defs
}

// RequiredScopeFor returns the scope required to call name, and whether name is known.
func (r *Registry) RequiredScopeFor(name string) (string, bool) {
	d, ok := r.byName[name]
	return d.RequiredScope, ok
}

func (r *Registry) ForScope(scope string) []ToolDef {
	callerRank := scopeRank(scope)
	out := make([]ToolDef, 0, len(r.defs))
	for _, d := range r.defs {
		if d.RequiredScope == "" {
			out = append(out, d)
			continue
		}
		if callerRank >= 0 && scopeRank(d.RequiredScope) <= callerRank {
			out = append(out, d)
		}
	}
	return out
}

func scopeRank(scope string) int {
	switch scope {
	case "":
		return 0
	case "read":
		return 0
	case "write":
		return 1
	default:
		return -1
	}
}

// ScopeRank returns the integer rank of a known scope:
//
//	0  anonymous / "" / read
//	1  write (highest)
//	0  unknown scope (treated as anonymous)
func ScopeRank(scope string) int {
	r := scopeRank(scope)
	if r < 0 {
		return 0
	}
	return r
}

// IsWriteScope reports whether scope carries write privileges.
func IsWriteScope(scope string) bool {
	return ScopeRank(scope) >= 1
}
