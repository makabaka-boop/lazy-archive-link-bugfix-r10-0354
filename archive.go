package main

import (
	"fmt"
	"sort"
)

type linkItem struct {
	Object  *inputObject  `json:"object,omitempty"`
	Archive *inputArchive `json:"archive,omitempty"`
}

type inputArchive struct {
	Name    string        `json:"name"`
	Members []inputObject `json:"members"`
}

type extraction struct {
	Archive string `json:"archive"`
	Member  string `json:"member"`
	Cause   string `json:"cause"`
}

type archiveMemberKey struct {
	archive int
	member  int
}

type archiveSelection struct {
	objects    []inputObject
	resolved   map[string]string
	unresolved map[string]struct{}
	selected   map[archiveMemberKey]struct{}
	evidence   []extraction
}

const (
	maxArchives = 8
	maxMembers  = 32
	maxObjects  = 64
)

// selectArchives implements command-order static archive semantics. Ordinary
// objects are always included. An archive is searched at its command position
// only; selecting a member adds its definitions before its references, and the
// same archive is rescanned until member-internal dependencies stop adding
// members.
func selectArchives(in inputFile) ([]inputObject, []extraction, error) {
	// The original objects-only interface remains unchanged, including its
	// lack of archive-specific limits.
	if in.Items == nil {
		return append([]inputObject(nil), in.Objects...), nil, nil
	}
	if len(in.Objects) != 0 {
		return nil, nil, fmt.Errorf("objects and items cannot both be present")
	}
	if err := validateItems(in); err != nil {
		return nil, nil, err
	}

	state := &archiveSelection{
		objects:    make([]inputObject, 0, len(in.Items)),
		resolved:   make(map[string]string),
		unresolved: make(map[string]struct{}),
		selected:   make(map[archiveMemberKey]struct{}),
	}

	for ai, item := range in.Items {
		switch {
		case item.Object != nil:
			if len(state.objects)+1 > maxObjects {
				return nil, nil, fmt.Errorf("too many selected objects: limit is %d", maxObjects)
			}
			state.addObject(*item.Object)

		case item.Archive != nil:
			if err := state.scanArchive(ai, item.Archive); err != nil {
				return nil, nil, err
			}
		}
	}

	return state.objects, state.evidence, nil
}

func validateItems(in inputFile) error {
	archiveCnt := 0
	memberCnt := 0
	objectCnt := 0
	archiveNames := make(map[string]int)
	for i, item := range in.Items {
		switch {
		case item.Object == nil && item.Archive == nil:
			return fmt.Errorf("item %d must contain exactly one object or archive", i)
		case item.Object != nil && item.Archive != nil:
			return fmt.Errorf("item %d must contain exactly one object or archive", i)
		case item.Archive != nil:
			archiveCnt++
			if item.Archive.Name == "" {
				return fmt.Errorf("archive %d has an empty name", i)
			}
			if first, ok := archiveNames[item.Archive.Name]; ok {
				return fmt.Errorf("archive name %q is duplicated by items %d and %d", item.Archive.Name, first, i)
			}
			archiveNames[item.Archive.Name] = i
			memberCnt += len(item.Archive.Members)
		default:
			objectCnt++
		}
	}
	if archiveCnt > maxArchives {
		return fmt.Errorf("too many archives: %d (limit %d)", archiveCnt, maxArchives)
	}
	if memberCnt > maxMembers {
		return fmt.Errorf("too many archive members: %d (limit %d)", memberCnt, maxMembers)
	}
	if objectCnt > maxObjects {
		return fmt.Errorf("too many objects: %d (limit %d)", objectCnt, maxObjects)
	}
	return nil
}

func (s *archiveSelection) scanArchive(archiveIndex int, archive *inputArchive) error {
	for {
		// Symbols made undefined by members selected during this scan are not
		// consulted until the next scan. That makes a rescan start at the
		// archive's first member and therefore preserve its first provider.
		scanUnresolved := make(map[string]struct{}, len(s.unresolved))
		for name := range s.unresolved {
			scanUnresolved[name] = struct{}{}
		}

		extracted := false
		for mi := range archive.Members {
			key := archiveMemberKey{archiveIndex, mi}
			if _, ok := s.selected[key]; ok {
				continue
			}

			member := archive.Members[mi]
			var triggers []string
			for _, name := range globalDefinitionNames(member) {
				if _, ok := scanUnresolved[name]; ok {
					triggers = append(triggers, name)
				}
			}
			if len(triggers) == 0 {
				continue
			}
			if len(s.objects)+1 > maxObjects {
				return fmt.Errorf("too many selected objects: limit is %d", maxObjects)
			}

			sort.Strings(triggers)
			s.selected[key] = struct{}{}
			s.evidence = append(s.evidence, extraction{
				Archive: archive.Name,
				Member:  member.Name,
				Cause:   triggers[0],
			})
			extracted = true
			s.addObject(member)

			// Once this member is selected, all of its global definitions take
			// part in the link and can satisfy references for the rest of scan.
			for _, name := range globalDefinitionNames(member) {
				delete(scanUnresolved, name)
			}
		}

		if !extracted {
			return nil
		}
	}
}

func (s *archiveSelection) addObject(obj inputObject) {
	s.objects = append(s.objects, obj)
	s.includeObject(obj)
}

// includeObject adds all global definitions and then all references. Local
// symbols are scoped to this object and never cause archive extraction.
func (s *archiveSelection) includeObject(obj inputObject) {
	for _, sym := range obj.Symbols {
		if sym.Name == "" || (sym.Binding != "strong" && sym.Binding != "weak") {
			continue
		}
		if sym.Binding == "strong" {
			s.resolved[sym.Name] = "strong"
		} else if _, ok := s.resolved[sym.Name]; !ok {
			s.resolved[sym.Name] = "weak"
		}
		delete(s.unresolved, sym.Name)
	}

	for _, rel := range obj.Relocations {
		if rel.Symbol == "" || hasLocalSymbol(obj, rel.Symbol) {
			continue
		}
		if _, ok := s.resolved[rel.Symbol]; !ok {
			s.unresolved[rel.Symbol] = struct{}{}
		}
	}
}

func globalDefinitionNames(obj inputObject) []string {
	names := make([]string, 0, len(obj.Symbols))
	for _, sym := range obj.Symbols {
		if sym.Name != "" && (sym.Binding == "strong" || sym.Binding == "weak") {
			names = append(names, sym.Name)
		}
	}
	return names
}

func hasLocalSymbol(obj inputObject, name string) bool {
	for _, sym := range obj.Symbols {
		if sym.Name == name && sym.Binding == "local" {
			return true
		}
	}
	return false
}
