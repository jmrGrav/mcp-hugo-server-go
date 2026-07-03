package tools

var scopeOrder = []string{"", "content.read", "content.write", "site.admin", "system.admin"}

type ToolDef struct {
	Name          string
	Description   string
	RequiredScope string
	InputSchema   any
}

type Registry struct {
	defs []ToolDef
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(def ToolDef) {
	r.defs = append(r.defs, def)
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
	for i, s := range scopeOrder {
		if s == scope {
			return i
		}
	}
	return -1
}

// ScopeRank returns the integer rank of a known scope:
//
//	0  anonymous / ""
//	1  content.read
//	2  content.write
//	3  site.admin
//	4  system.admin
//	0  unknown scope (treated as anonymous)
func ScopeRank(scope string) int {
	r := scopeRank(scope)
	if r < 0 {
		return 0
	}
	return r
}
