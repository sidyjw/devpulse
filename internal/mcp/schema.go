package mcp

// ---------- JSON schema helpers for tool input schemas ----------

func Obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func StrP(d string) map[string]any  { return map[string]any{"type": "string", "description": d} }
func NumP(d string) map[string]any  { return map[string]any{"type": "number", "description": d} }
func IntP(d string) map[string]any  { return map[string]any{"type": "integer", "description": d} }
func BoolP(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
func ArrP(item, d string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": item}, "description": d}
}
func EnumP(d string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals, "description": d}
}

// Common annotations.
var (
	ReadOnly    = &ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true}
	WriteTool   = &ToolAnnotations{OpenWorldHint: true}
	Destructive = &ToolAnnotations{DestructiveHint: true, OpenWorldHint: true}
)
