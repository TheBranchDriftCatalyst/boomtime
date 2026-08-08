package climeta

// bind.go — the typed binder for POST /api/v1/admin/cli/run. The wire body
// carries ONE flat `flags` object (positional params are keyed by name just
// like flags); BindRunArgs validates every key against the CommandSpec and
// coerces each value to its declared type. Anything unknown, mistyped, or
// missing-and-required is a hard reject — the invokers only ever see typed,
// validated arguments.

import (
	"fmt"
	"math"
	"strings"
)

// DryRunFlag is the canonical name of the dry-run flag on mutating commands
// that support previewing (matches the cobra flag name).
const DryRunFlag = "dry-run"

// BindRunArgs validates raw (param name → JSON-decoded value) against spec
// and returns typed RunArgs. For a mutating command with DryRunSupported, an
// absent dry-run defaults to TRUE — the web surface previews unless the
// caller explicitly applies. The caller's map is never mutated.
func BindRunArgs(spec CommandSpec, raw map[string]any) (RunArgs, error) {
	byName := make(map[string]CmdParam, len(spec.Params))
	for _, p := range spec.Params {
		byName[p.Name] = p
	}

	for k := range raw {
		if _, ok := byName[k]; !ok {
			return RunArgs{}, fmt.Errorf("unknown parameter %q (valid: %s)", k, paramNames(spec))
		}
	}

	vals := make(map[string]any, len(raw)+1)
	for k, v := range raw {
		vals[k] = v
	}
	if spec.Classification == ClassMutating && spec.DryRunSupported {
		if _, ok := vals[DryRunFlag]; !ok {
			vals[DryRunFlag] = true
		}
	}

	args := RunArgs{Flags: map[string]any{}}
	for _, p := range spec.Params {
		v, present := vals[p.Name]
		if !present {
			if p.Required {
				return RunArgs{}, fmt.Errorf("missing required parameter %q", p.Name)
			}
			if p.Positional {
				// Keep positional indexing stable for any trailing optionals.
				args.Positional = append(args.Positional, "")
			}
			continue
		}
		coerced, err := coerceParam(p, v)
		if err != nil {
			return RunArgs{}, err
		}
		if p.Positional {
			s, _ := coerced.(string)
			args.Positional = append(args.Positional, s)
		} else {
			args.Flags[p.Name] = coerced
		}
	}
	return args, nil
}

// coerceParam converts one JSON-decoded value to the param's declared type.
// Strict on purpose: "true" is not a bool, 1 is not a string — the FE sends
// typed JSON and anything else is a bug worth surfacing as a 400.
func coerceParam(p CmdParam, v any) (any, error) {
	switch p.Type {
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a boolean", p.Name)
		}
		return b, nil
	case "int":
		// encoding/json decodes every number to float64; accept only whole
		// values that round-trip.
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) {
			return nil, fmt.Errorf("parameter %q must be an integer", p.Name)
		}
		return int(f), nil
	case "stringSlice":
		items, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be an array of strings", p.Name)
		}
		out := make([]string, len(items))
		for i, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("parameter %q must be an array of strings", p.Name)
			}
			out[i] = s
		}
		return out, nil
	case "enum":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a string", p.Name)
		}
		for _, allowed := range p.Enum {
			if s == allowed {
				return s, nil
			}
		}
		return nil, fmt.Errorf("parameter %q must be one of [%s]", p.Name, strings.Join(p.Enum, ", "))
	default: // "string" + unrecognized degrade to string
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a string", p.Name)
		}
		return s, nil
	}
}

func paramNames(spec CommandSpec) string {
	names := make([]string, len(spec.Params))
	for i, p := range spec.Params {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
