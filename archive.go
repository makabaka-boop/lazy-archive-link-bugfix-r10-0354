package main

import (
	"errors"
	"fmt"
)

// Limits for the items/archive interface. They bound archive expansion and
// apply only when items are used; a plain objects input links exactly as
// before.
const (
	maxArchives     = 8  // archives per input
	maxTotalMembers = 32 // members summed over all archives
	maxFinalObjects = 64 // direct objects plus extracted members
)

type linkItem struct {
	Object  *inputObject  `json:"object,omitempty"`
	Archive *inputArchive `json:"archive,omitempty"`
}

type inputArchive struct {
	Name    string        `json:"name"`
	Members []inputObject `json:"members"`
}

// extraction records why an archive member joined the link. Cause is the
// smallest symbol name whose unresolved reference first pulled the member in.
type extraction struct {
	Archive string `json:"archive"`
	Member  string `json:"member"`
	Cause   string `json:"cause"`
}

// symbolDemand tracks the global symbol state while items are scanned in
// command order: definitions already available from loaded objects and
// relocation references still waiting for a definition.
type symbolDemand struct {
	defined   map[string]bool
	undefined map[string]bool
}

// load folds one object into the symbol state. Its non-local definitions
// satisfy pending references (an existing weak definition counts as
// satisfying), and its relocations become new demands unless the same object
// covers them with a local definition or they are already defined.
func (d *symbolDemand) load(obj *inputObject) {
	locals := make(map[string]bool, len(obj.Symbols))
	for _, s := range obj.Symbols {
		if s.Binding == "local" {
			locals[s.Name] = true
		}
	}
	for _, s := range obj.Symbols {
		if s.Binding == "local" {
			continue
		}
		d.defined[s.Name] = true
		delete(d.undefined, s.Name)
	}
	for _, r := range obj.Relocations {
		if locals[r.Symbol] || d.defined[r.Symbol] {
			continue
		}
		d.undefined[r.Symbol] = true
	}
}

// cause returns the smallest undefined symbol name that the member could
// satisfy with a non-local definition. The second result is false when the
// member satisfies nothing; local definitions never satisfy another object's
// reference.
func (d *symbolDemand) cause(member *inputObject) (string, bool) {
	best := ""
	found := false
	for _, s := range member.Symbols {
		if s.Binding == "local" || !d.undefined[s.Name] {
			continue
		}
		if !found || s.Name < best {
			best, found = s.Name, true
		}
	}
	return best, found
}

// selectArchives flattens items into the final object list. Regular objects
// are appended where they appear. An archive is scanned only at its own
// position: a member joins the link when one of its non-local definitions
// satisfies a reference that is unresolved at that moment, and the archive is
// rescanned until a full pass extracts nothing. Extracting a member can
// uncover references that only other members satisfy, so dependencies between
// members (including cycles) converge; each member joins at most once, which
// also guarantees the scan terminates. A scanned archive is never revisited,
// so later objects cannot pull members out of earlier archives.
func selectArchives(in inputFile) ([]inputObject, []extraction, error) {
	if len(in.Items) == 0 {
		// The archive interface is unused; the link must behave exactly as
		// before.
		return append([]inputObject(nil), in.Objects...), nil, nil
	}
	if len(in.Objects) > 0 {
		return nil, nil, errors.New("cannot mix objects and items")
	}

	objects := make([]inputObject, 0, len(in.Items))
	var evidence []extraction
	demand := &symbolDemand{
		defined:   make(map[string]bool),
		undefined: make(map[string]bool),
	}
	archiveNames := make(map[string]bool)
	archiveCount, memberCount := 0, 0

	for i := range in.Items {
		item := &in.Items[i]
		if (item.Object == nil) == (item.Archive == nil) {
			return nil, nil, fmt.Errorf("item %d must contain exactly one of object or archive", i)
		}
		if item.Object != nil {
			obj := *item.Object
			objects = append(objects, obj)
			demand.load(&obj)
			continue
		}

		archive := item.Archive
		archiveCount++
		if archiveCount > maxArchives {
			return nil, nil, fmt.Errorf("item %d: more than %d archives", i, maxArchives)
		}
		if archive.Name == "" {
			return nil, nil, fmt.Errorf("item %d: archive must have a name", i)
		}
		if archiveNames[archive.Name] {
			return nil, nil, fmt.Errorf("item %d: duplicate archive name %q", i, archive.Name)
		}
		archiveNames[archive.Name] = true
		memberCount += len(archive.Members)
		if memberCount > maxTotalMembers {
			return nil, nil, fmt.Errorf(
				"item %d: archive %q pushes the member total to %d, over the limit of %d",
				i, archive.Name, memberCount, maxTotalMembers,
			)
		}

		extracted := make([]bool, len(archive.Members))
		for {
			progress := false
			for mi := range archive.Members {
				if extracted[mi] {
					continue
				}
				member := &archive.Members[mi]
				cause, ok := demand.cause(member)
				if !ok {
					continue
				}
				extracted[mi] = true
				progress = true
				objects = append(objects, *member)
				evidence = append(evidence, extraction{
					Archive: archive.Name,
					Member:  member.Name,
					Cause:   cause,
				})
				demand.load(member)
			}
			if !progress {
				break
			}
		}
	}

	if len(objects) > maxFinalObjects {
		return nil, nil, fmt.Errorf("items select %d final objects, over the limit of %d", len(objects), maxFinalObjects)
	}
	return objects, evidence, nil
}
