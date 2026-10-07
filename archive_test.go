package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func checkImage(t *testing.T, got []byte, wantHex string) {
	t.Helper()
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatalf("bad hex %q: %v", wantHex, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("image = %x, want %s", got, wantHex)
	}
}

func abs32ref(section string, offset int64, symbol string) inputReloc {
	return inputReloc{Section: section, Offset: offset, Type: "ABS32", Symbol: symbol}
}

func TestArchiveExtractsOnlyNeededMembers(t *testing.T) {
	in := inputFile{Items: []linkItem{
		{Object: &inputObject{
			Name:        "main",
			Text:        ByteSlice{1, 2, 3, 4},
			Relocations: []inputReloc{abs32ref("text", 0, "foo")},
		}},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{
				{
					Name:    "foo.o",
					Text:    ByteSlice{9, 9, 9, 9},
					Symbols: []inputSymbol{{Name: "foo", Binding: "strong", Section: "text", Value: 0}},
				},
				{
					Name:    "unused.o",
					Text:    ByteSlice{7, 7, 7, 7},
					Symbols: []inputSymbol{{Name: "bar", Binding: "strong", Section: "text", Value: 0}},
				},
			},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "0410000009090909")

	if len(report.Extracted) != 1 ||
		report.Extracted[0] != (extraction{Archive: "liba", Member: "foo.o", Cause: "foo"}) {
		t.Fatalf("extracted = %+v", report.Extracted)
	}
	if len(report.Layout) != 2 || report.Layout[1].ObjectName != "foo.o" || int(report.Layout[1].ImageOffset) != 4 {
		t.Fatalf("layout = %+v", report.Layout)
	}
	if got := int(report.SymbolAddrs.Global["foo"]); got != 0x1004 {
		t.Fatalf("foo = 0x%x, want 0x1004", got)
	}
	for _, s := range report.Symbols {
		if s.Name == "bar" {
			t.Fatalf("unused member symbol leaked into report: %+v", s)
		}
	}
	if len(report.Relocations) != 1 || report.Relocations[0].SymbolObject != 1 {
		t.Fatalf("relocations = %+v", report.Relocations)
	}
}

func TestUnusedMemberDuplicateStrongSymbolsIgnored(t *testing.T) {
	in := inputFile{Items: []linkItem{
		{Object: &inputObject{
			Name:        "main",
			Text:        ByteSlice{1, 2, 3, 4},
			Symbols:     []inputSymbol{{Name: "self", Binding: "strong", Section: "text", Value: 0}},
			Relocations: []inputReloc{abs32ref("text", 0, "foo")},
		}},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{
				{
					Name:    "one.o",
					Text:    ByteSlice{0x11},
					Symbols: []inputSymbol{{Name: "foo", Binding: "strong", Section: "text", Value: 0}},
				},
				{
					// Would collide with both main's self and one.o's foo if
					// it were linked, but nothing needs it.
					Name: "two.o",
					Text: ByteSlice{0x22},
					Symbols: []inputSymbol{
						{Name: "foo", Binding: "strong", Section: "text", Value: 0},
						{Name: "self", Binding: "strong", Section: "text", Value: 0},
					},
				},
			},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "0410000011")
	if len(report.Extracted) != 1 || report.Extracted[0].Member != "one.o" {
		t.Fatalf("extracted = %+v", report.Extracted)
	}
	if got := int(report.SymbolAddrs.Global["self"]); got != 0x1000 {
		t.Fatalf("self = 0x%x, want 0x1000", got)
	}
}

func TestArchiveMemberDependenciesTakeMultiplePasses(t *testing.T) {
	// b.o sits before a.o in the archive, but only a.o satisfies the initial
	// undefined reference. Extracting a.o creates the demand for b, so a
	// second pass over the archive is required.
	in := inputFile{Items: []linkItem{
		{Object: &inputObject{
			Name:        "main",
			Text:        ByteSlice{0, 0, 0, 0},
			Relocations: []inputReloc{abs32ref("text", 0, "a")},
		}},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{
				{
					Name:    "b.o",
					Text:    ByteSlice{0xbb, 0xbb},
					Symbols: []inputSymbol{{Name: "b", Binding: "strong", Section: "text", Value: 0}},
				},
				{
					Name:    "a.o",
					Text:    ByteSlice{0, 0},
					Symbols: []inputSymbol{{Name: "a", Binding: "strong", Section: "text", Value: 0}},
					Relocations: []inputReloc{
						{Section: "text", Offset: 0, Type: "PCREL16", Symbol: "b"},
					},
				},
			},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "041000000000bbbb")
	want := []extraction{
		{Archive: "liba", Member: "a.o", Cause: "a"},
		{Archive: "liba", Member: "b.o", Cause: "b"},
	}
	if !reflect.DeepEqual(report.Extracted, want) {
		t.Fatalf("extracted = %+v, want %+v", report.Extracted, want)
	}
}

func TestArchiveBeforeReferenceIsNotRescanned(t *testing.T) {
	// The archive is scanned before main's reference to foo exists, so it
	// contributes nothing and the later reference stays unresolved.
	in := inputFile{Items: []linkItem{
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{{
				Name:    "foo.o",
				Text:    ByteSlice{9},
				Symbols: []inputSymbol{{Name: "foo", Binding: "strong", Section: "text", Value: 0}},
			}},
		}},
		{Object: &inputObject{
			Name:        "main",
			Text:        ByteSlice{1, 2, 3, 4},
			Relocations: []inputReloc{abs32ref("text", 0, "foo")},
		}},
	}}
	image, _, err := link(marshalInput(t, in))
	if err == nil || !strings.Contains(err.Error(), `unresolved symbol "foo"`) {
		t.Fatalf("error = %v, want unresolved symbol", err)
	}
	if image != nil {
		t.Fatalf("failed link produced an image")
	}
}

func TestExistingWeakDefinitionCountsAsSatisfied(t *testing.T) {
	in := inputFile{Items: []linkItem{
		{Object: &inputObject{
			Name:    "weak-owner",
			Text:    ByteSlice{0xaa, 0xbb},
			Symbols: []inputSymbol{{Name: "W", Binding: "weak", Section: "text", Value: 0}},
		}},
		{Object: &inputObject{
			Name:        "user",
			Text:        ByteSlice{1, 2, 3, 4},
			Relocations: []inputReloc{abs32ref("text", 0, "W")},
		}},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{{
				Name:    "strong.o",
				Text:    ByteSlice{9},
				Symbols: []inputSymbol{{Name: "W", Binding: "strong", Section: "text", Value: 0}},
			}},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "aabb00100000")
	if len(report.Extracted) != 0 {
		t.Fatalf("extracted = %+v, want none", report.Extracted)
	}
	if got := int(report.SymbolAddrs.Global["W"]); got != 0x1000 {
		t.Fatalf("W = 0x%x, want the existing weak definition at 0x1000", got)
	}
}

func TestExtractedStrongDefinitionOverridesWeak(t *testing.T) {
	// The member's strong W cannot pull it in while weak W satisfies the
	// reference, but once X pulls the member in, its strong W wins.
	in := inputFile{Items: []linkItem{
		{Object: &inputObject{
			Name:    "weak-owner",
			Text:    ByteSlice{0xaa, 0xbb},
			Symbols: []inputSymbol{{Name: "W", Binding: "weak", Section: "text", Value: 0}},
		}},
		{Object: &inputObject{
			Name:        "user",
			Text:        ByteSlice{1, 2, 3, 4},
			Relocations: []inputReloc{abs32ref("text", 0, "X")},
		}},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{{
				Name: "both.o",
				Text: ByteSlice{5, 6, 7, 8},
				Symbols: []inputSymbol{
					{Name: "X", Binding: "strong", Section: "text", Value: 0},
					{Name: "W", Binding: "strong", Section: "text", Value: 2},
				},
			}},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "aabb0610000005060708")
	if len(report.Extracted) != 1 || report.Extracted[0].Cause != "X" {
		t.Fatalf("extracted = %+v", report.Extracted)
	}
	if got := int(report.SymbolAddrs.Global["W"]); got != 0x1008 {
		t.Fatalf("W = 0x%x, want the extracted strong definition at 0x1008", got)
	}
	var weakW, strongW SymbolReport
	for _, s := range report.Symbols {
		if s.Name != "W" {
			continue
		}
		if s.Binding == "weak" {
			weakW = s
		} else {
			strongW = s
		}
	}
	if weakW.Selected || !strongW.Selected {
		t.Fatalf("weak = %+v, strong = %+v", weakW, strongW)
	}
}

func TestLocalSymbolsDoNotDriveExtraction(t *testing.T) {
	t.Run("local reference is already resolved", func(t *testing.T) {
		in := inputFile{Items: []linkItem{
			{Object: &inputObject{
				Name:        "main",
				Text:        ByteSlice{1, 2, 3, 4},
				Symbols:     []inputSymbol{{Name: "foo", Binding: "local", Section: "text", Value: 0}},
				Relocations: []inputReloc{abs32ref("text", 0, "foo")},
			}},
			{Archive: &inputArchive{
				Name: "liba",
				Members: []inputObject{{
					Name:    "foo.o",
					Text:    ByteSlice{9},
					Symbols: []inputSymbol{{Name: "foo", Binding: "strong", Section: "text", Value: 0}},
				}},
			}},
		}}
		image, report, err := link(marshalInput(t, in))
		if err != nil {
			t.Fatal(err)
		}
		checkImage(t, image, "00100000")
		if len(report.Extracted) != 0 {
			t.Fatalf("extracted = %+v, want none", report.Extracted)
		}
		if len(report.SymbolAddrs.Global) != 0 {
			t.Fatalf("globals = %v, want none", report.SymbolAddrs.Global)
		}
	})

	t.Run("member local definition does not satisfy", func(t *testing.T) {
		in := inputFile{Items: []linkItem{
			{Object: &inputObject{
				Name:        "main",
				Text:        ByteSlice{1, 2, 3, 4},
				Relocations: []inputReloc{abs32ref("text", 0, "foo")},
			}},
			{Archive: &inputArchive{
				Name: "liba",
				Members: []inputObject{{
					Name:    "foo.o",
					Text:    ByteSlice{9},
					Symbols: []inputSymbol{{Name: "foo", Binding: "local", Section: "text", Value: 0}},
				}},
			}},
		}}
		image, _, err := link(marshalInput(t, in))
		if err == nil || !strings.Contains(err.Error(), `unresolved symbol "foo"`) {
			t.Fatalf("error = %v, want unresolved symbol", err)
		}
		if image != nil {
			t.Fatalf("failed link produced an image")
		}
	})
}

func TestCyclicMemberDependencyTerminates(t *testing.T) {
	in := inputFile{Items: []linkItem{
		{Object: &inputObject{
			Name:        "main",
			Text:        ByteSlice{0, 0, 0, 0},
			Relocations: []inputReloc{abs32ref("text", 0, "x")},
		}},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{
				{
					Name:    "a.o",
					Text:    ByteSlice{0, 0},
					Symbols: []inputSymbol{{Name: "x", Binding: "strong", Section: "text", Value: 0}},
					Relocations: []inputReloc{
						{Section: "text", Offset: 0, Type: "PCREL16", Symbol: "y"},
					},
				},
				{
					Name:    "b.o",
					Text:    ByteSlice{0, 0},
					Symbols: []inputSymbol{{Name: "y", Binding: "strong", Section: "text", Value: 0}},
					Relocations: []inputReloc{
						{Section: "text", Offset: 0, Type: "PCREL16", Symbol: "x"},
					},
				},
			},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "041000000000fcff")
	if len(report.Extracted) != 2 ||
		report.Extracted[0] != (extraction{Archive: "liba", Member: "a.o", Cause: "x"}) ||
		report.Extracted[1] != (extraction{Archive: "liba", Member: "b.o", Cause: "y"}) {
		t.Fatalf("extracted = %+v", report.Extracted)
	}
}

func TestMemberExtractedAtMostOnce(t *testing.T) {
	ref := func(name string, fill byte) *inputObject {
		return &inputObject{
			Name:        name,
			Text:        ByteSlice{fill, fill, fill, fill},
			Relocations: []inputReloc{abs32ref("text", 0, "foo")},
		}
	}
	in := inputFile{Items: []linkItem{
		{Object: ref("one", 1)},
		{Object: ref("two", 2)},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{{
				Name:    "foo.o",
				Text:    ByteSlice{9},
				Symbols: []inputSymbol{{Name: "foo", Binding: "strong", Section: "text", Value: 0}},
			}},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "081000000810000009")
	if len(report.Extracted) != 1 {
		t.Fatalf("extracted = %+v, want exactly one", report.Extracted)
	}
}

func TestExtractionCauseIsSmallestSatisfiedSymbol(t *testing.T) {
	in := inputFile{Items: []linkItem{
		{Object: &inputObject{
			Name: "main",
			Text: ByteSlice{0, 0, 0, 0, 0, 0, 0, 0},
			Relocations: []inputReloc{
				abs32ref("text", 0, "zed"),
				abs32ref("text", 4, "abc"),
			},
		}},
		{Archive: &inputArchive{
			Name: "liba",
			Members: []inputObject{{
				Name: "both.o",
				Text: ByteSlice{1, 2},
				Symbols: []inputSymbol{
					{Name: "zed", Binding: "strong", Section: "text", Value: 0},
					{Name: "abc", Binding: "strong", Section: "text", Value: 1},
				},
			}},
		}},
	}}
	image, report, err := link(marshalInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	checkImage(t, image, "08100000091000000102")
	if len(report.Extracted) != 1 || report.Extracted[0].Cause != "abc" {
		t.Fatalf("extracted = %+v, want cause abc", report.Extracted)
	}
}

func TestItemsInputValidation(t *testing.T) {
	obj := inputObject{Text: ByteSlice{1}}
	arch := func(name string, members int) *inputArchive {
		a := &inputArchive{Name: name}
		for i := 0; i < members; i++ {
			a.Members = append(a.Members, inputObject{Name: fmt.Sprintf("%s-%d", name, i)})
		}
		return a
	}
	manyObjects := inputFile{}
	for i := 0; i < 65; i++ {
		manyObjects.Items = append(manyObjects.Items, linkItem{Object: &inputObject{Text: ByteSlice{1}}})
	}

	tests := []struct {
		name string
		in   inputFile
		want string
	}{
		{
			name: "objects mixed with items",
			in: inputFile{
				Objects: []inputObject{obj},
				Items:   []linkItem{{Object: &obj}},
			},
			want: "mix",
		},
		{
			name: "item with neither",
			in:   inputFile{Items: []linkItem{{}}},
			want: "exactly one",
		},
		{
			name: "item with both",
			in:   inputFile{Items: []linkItem{{Object: &obj, Archive: arch("a", 0)}}},
			want: "exactly one",
		},
		{
			name: "archive without name",
			in:   inputFile{Items: []linkItem{{Archive: arch("", 0)}}},
			want: "must have a name",
		},
		{
			name: "duplicate archive name",
			in: inputFile{Items: []linkItem{
				{Archive: arch("dup", 0)},
				{Archive: arch("dup", 0)},
			}},
			want: "duplicate archive name",
		},
		{
			name: "too many archives",
			in: inputFile{Items: []linkItem{
				{Archive: arch("a1", 0)}, {Archive: arch("a2", 0)}, {Archive: arch("a3", 0)},
				{Archive: arch("a4", 0)}, {Archive: arch("a5", 0)}, {Archive: arch("a6", 0)},
				{Archive: arch("a7", 0)}, {Archive: arch("a8", 0)}, {Archive: arch("a9", 0)},
			}},
			want: "archives",
		},
		{
			name: "too many members",
			in:   inputFile{Items: []linkItem{{Archive: arch("big", 33)}}},
			want: "member",
		},
		{
			name: "too many final objects",
			in:   manyObjects,
			want: "64",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image, _, err := link(marshalInput(t, tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if image != nil {
				t.Fatalf("invalid input produced an image")
			}
		})
	}
}

func TestItemsWithoutArchivesMatchObjectsMode(t *testing.T) {
	objs := []inputObject{
		{
			Name:        "one",
			Text:        ByteSlice{1, 2, 3, 4},
			Symbols:     []inputSymbol{{Name: "X", Binding: "strong", Section: "text", Value: 0}},
			Relocations: []inputReloc{abs32ref("text", 0, "X")},
		},
		{Name: "two", Align: ptr[int64](4), Data: ByteSlice{0xee}},
	}
	imageFromItems, reportFromItems, err := link(marshalInput(t, inputFile{
		Items: []linkItem{{Object: &objs[0]}, {Object: &objs[1]}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	imageFromObjects, reportFromObjects, err := link(marshalInput(t, inputFile{Objects: objs}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(imageFromItems, imageFromObjects) {
		t.Fatalf("images differ: %x vs %x", imageFromItems, imageFromObjects)
	}
	if !reflect.DeepEqual(reportFromItems, reportFromObjects) {
		t.Fatalf("reports differ:\nitems:   %+v\nobjects: %+v", reportFromItems, reportFromObjects)
	}
}
