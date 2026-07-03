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
