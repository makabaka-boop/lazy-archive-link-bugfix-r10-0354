package main

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

func selectArchives(in inputFile) ([]inputObject, []extraction, error) {
	objects := append([]inputObject(nil), in.Objects...)
	evidence := []extraction{}
	for _, item := range in.Items {
		if item.Object != nil {
			objects = append(objects, *item.Object)
		}
		if item.Archive != nil {
			for _, member := range item.Archive.Members {
				objects = append(objects, member)
				evidence = append(evidence, extraction{item.Archive.Name, member.Name, "included"})
			}
		}
	}
	return objects, evidence, nil
}
