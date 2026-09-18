package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// validatePropertyTextValue validates and canonicalizes a "text" property value.
func validatePropertyTextValue(v any) ([]byte, error) {
	s, ok := v.(string)
	if !ok {
		return nil, errors.New("value must be a string")
	}
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("value cannot be empty (use DELETE to unset a property)")
	}
	if utf8.RuneCountInString(s) > maxPropertyTextValueLen {
		return nil, fmt.Errorf("value must be %d characters or fewer", maxPropertyTextValueLen)
	}
	return json.Marshal(sanitizeNullBytes(s))
}

// validatePropertyURLValue validates and canonicalizes a "url" property value.
func validatePropertyURLValue(v any) ([]byte, error) {
	s, ok := v.(string)
	if !ok {
		return nil, errors.New("value must be a URL string")
	}
	s = strings.TrimSpace(s)
	if len(s) > maxPropertyURLValueLen {
		return nil, fmt.Errorf("value must be %d characters or fewer", maxPropertyURLValueLen)
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("value must be an http(s) URL")
	}
	return json.Marshal(s)
}

// validatePropertyNumberValue validates and canonicalizes a "number" property value.
func validatePropertyNumberValue(v any) ([]byte, error) {
	if _, ok := v.(float64); !ok {
		return nil, errors.New("value must be a number")
	}
	return json.Marshal(v)
}

// validatePropertyCheckboxValue validates and canonicalizes a "checkbox" property value.
func validatePropertyCheckboxValue(v any) ([]byte, error) {
	if _, ok := v.(bool); !ok {
		return nil, errors.New("value must be true or false")
	}
	return json.Marshal(v)
}

// validatePropertyDateValue validates and canonicalizes a "date" property value.
func validatePropertyDateValue(v any) ([]byte, error) {
	s, ok := v.(string)
	if !ok {
		return nil, errors.New("value must be a date string in YYYY-MM-DD format")
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return nil, errors.New("value must be a date string in YYYY-MM-DD format")
	}
	return json.Marshal(s)
}

// validatePropertySelectValue validates and canonicalizes a "select" property value.
func validatePropertySelectValue(v any, cfg PropertyConfig) ([]byte, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("value must be one of the option ids: %s", selectOptionsHint(cfg))
	}
	if _, exists := propertyOptionIDs(cfg)[s]; !exists {
		return nil, fmt.Errorf("value must be one of the option ids: %s", selectOptionsHint(cfg))
	}
	return json.Marshal(s)
}

// validatePropertyMultiSelectValue validates and canonicalizes a "multi_select" property value.
// It deduplicates entries and canonicalizes to config order for stable @> containment filtering.
func validatePropertyMultiSelectValue(v any, cfg PropertyConfig) ([]byte, error) {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("value must be a non-empty array of option ids: %s", selectOptionsHint(cfg))
	}
	order := propertyOptionIDs(cfg)
	seen := make(map[string]struct{}, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("value must be a non-empty array of option ids: %s", selectOptionsHint(cfg))
		}
		if _, exists := order[s]; !exists {
			return nil, fmt.Errorf("unknown option id %q; valid option ids: %s", s, selectOptionsHint(cfg))
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		ids = append(ids, s)
	}
	// Canonicalize to config order so equal selections serialize equally
	// (stable @> containment filtering and change detection).
	sort.SliceStable(ids, func(a, b int) bool { return order[ids[a]] < order[ids[b]] })
	return json.Marshal(ids)
}

// validatePropertyActorValue validates and canonicalizes an "actor" property value.
func validatePropertyActorValue(v any) ([]byte, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("value must be an actor reference string like \"member:<uuid>\" (kinds: %s)", actorKindsHint())
	}
	ref, err := parseActorRef(s)
	if err != nil {
		return nil, err
	}
	return json.Marshal(ref.String())
}

// validatePropertyMultiActorValue validates and canonicalizes a "multi_actor" property value.
func validatePropertyMultiActorValue(v any) ([]byte, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("value must be an array of actor reference strings like \"member:<uuid>\" (kinds: %s)", actorKindsHint())
	}
	refs, err := parseActorRefList(items)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.String()
	}
	return json.Marshal(out)
}