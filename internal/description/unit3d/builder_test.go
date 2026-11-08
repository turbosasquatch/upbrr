// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNormalizeDescriptionPreservesLiteralAndComparisonWhitespace(t *testing.T) {
	t.Parallel()
	for _, block := range []string{
		"[code]first\n\n\n  indented line[/code]",
		"[pre]first\n\n\n  indented line[/pre]",
		"[comparison=Source,Encode]first\n\n\n  indented line[/comparison]",
		"[comparison=Source,Encode][code]first\n\n\n  indented line[/code][/comparison]",
	} {
		input := " \nBefore\n\n\n" + block + "\n\n\nAfter\n "
		want := "Before\n\n" + block + "\n\nAfter"
		if got := normalizeDescription(input); got != want {
			t.Errorf("normalized description = %q, want %q", got, want)
		}
	}
}

func TestBuildDescriptionKeepsComparisonImagesWhenReplacingScreenshots(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[align=left]Source &amp; Encode[/align]\r\n\r\n\r\n" +
		"[center][url=https://img.example/comparison-page][img width=320]https://img.example/comparison.png[/img][/url][/center]\r\n[/spoiler]"
	kept := "Release notes\n\n" + comparison + "\n\n[center][img]https://img.example/old-screen.png[/img][/center]"
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, config.Config{}, config.TrackerConfig{},
		api.NopLogger{}, kept, nil, []api.ScreenshotImage{{ImgURL: "https://img.example/new-screen.png"}})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	if !strings.Contains(description, comparison) {
		t.Fatalf("comparison block was changed or removed: %q", description)
	}
	if strings.Contains(description, "old-screen.png") || !strings.Contains(description, "new-screen.png") {
		t.Fatalf("screenshot replacement failed: %q", description)
	}
}

func TestBuildScreenshotTagEmbedsFullSizeImageAtDisplayWidth(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		image api.ScreenshotImage
		want  string
	}{
		{"host page", api.ScreenshotImage{
			ImgURL: "https://images.example.invalid/thumb.png",
			RawURL: "https://images.example.invalid/full.png",
			WebURL: "https://images.example.invalid/page",
		}, "[url=https://images.example.invalid/page][img=350]https://images.example.invalid/full.png[/img][/url]"},
		{"direct full image", api.ScreenshotImage{ImgURL: "https://images.example.invalid/thumb.png", RawURL: "https://images.example.invalid/full.png"}, "[url=https://images.example.invalid/full.png][img=350]https://images.example.invalid/full.png[/img][/url]"},
		{"no thumbnail", api.ScreenshotImage{RawURL: "https://images.example.invalid/full.png"}, "[url=https://images.example.invalid/full.png][img=350]https://images.example.invalid/full.png[/img][/url]"},
		{"thumbnail fallback", api.ScreenshotImage{
			RawURL: " ",
			ImgURL: "https://images.example.invalid/thumb.png",
			WebURL: "https://images.example.invalid/page",
		}, "[url=https://images.example.invalid/page][img=350]https://images.example.invalid/thumb.png[/img][/url]"},
		{"web fallback", api.ScreenshotImage{WebURL: "https://images.example.invalid/page"}, "[url=https://images.example.invalid/page][img=350]https://images.example.invalid/page[/img][/url]"},
		{"empty", api.ScreenshotImage{}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := buildScreenshotTag(test.image, 350); got != test.want {
				t.Fatalf("screenshot tag = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildDescriptionDoesNotDeduplicateSelectedScreenshotAgainstComparison(t *testing.T) {
	imageURL := "https://img.example/shared.png"
	comparison := "[spoiler=Comparisons][img]" + imageURL + "[/img][/spoiler]"
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, config.Config{}, config.TrackerConfig{},
		api.NopLogger{}, comparison, nil, []api.ScreenshotImage{{ImgURL: imageURL}})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	if !strings.Contains(description, comparison) || strings.Count(description, imageURL) != 2 {
		t.Fatalf("comparison hid separately selected screenshot: %q", description)
	}
}

func TestBuildDescriptionKeepsNestedSourceAudioInComparison(t *testing.T) {
	comparison := "[spoiler=Comparisons]\n[spoiler=Audio Plots]copied audio[/spoiler]\n[/spoiler]"
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, config.Config{}, config.TrackerConfig{},
		api.NopLogger{}, "Notes\n\n"+comparison, nil, nil)
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	if !strings.Contains(description, comparison) {
		t.Fatalf("nested comparison changed: %q", description)
	}
}

func TestBuildDescriptionPlacesAudioAfterMenusBeforeScreenshots(t *testing.T) {
	t.Parallel()
	audio := "[spoiler=Audio Plots]\n[img]https://img.example/audio.png[/img]\n[code]Peak: -1 dB[/code]\n[/spoiler]"
	cfg := config.Config{}
	cfg.Description.ThumbnailSize = 420
	description, err := BuildDescription(t.Context(), api.DescriptionSubject{}, cfg,
		config.TrackerConfig{}, api.NopLogger{}, "Base description\n\n"+audio,
		[]api.ScreenshotImage{{ImgURL: "https://img.example/menu.png"}},
		[]api.ScreenshotImage{{ImgURL: "https://img.example/screen.png"}})
	if err != nil {
		t.Fatal(err)
	}
	basePos := strings.Index(description, "Base description")
	menuPos := strings.Index(description, "https://img.example/menu.png")
	audioPos := strings.Index(description, "https://img.example/audio.png")
	screenPos := strings.Index(description, "https://img.example/screen.png")
	if basePos < 0 || menuPos <= basePos || audioPos <= menuPos || screenPos <= audioPos ||
		!strings.Contains(description, "[spoiler=Audio Plots]") ||
		!strings.Contains(description, "[img=420]https://img.example/audio.png[/img]") ||
		!strings.Contains(description, "[img=420]https://img.example/screen.png[/img]") ||
		!strings.Contains(description, "[code]Peak: -1 dB[/code]") {
		t.Fatalf("audio analysis placement = %q", description)
	}
}

func TestBuildDiscScreenshotSectionsGroupsPreparedDiscOrder(t *testing.T) {
	t.Parallel()

	meta := api.DescriptionSubject{Disc: api.DiscFacts{Items: []api.DiscItemFacts{
		{
			ID:   "disc-one",
			Name: "Disc 1",
			Type: "BDMV",
		},
		{
			ID:   "disc-two",
			Name: "Disc 2",
			Type: "BDMV",
		},
	}}}
	images := []api.ScreenshotImage{
		{
			DiscID: "disc-two",
			RawURL: "https://images.example.invalid/disc-two.png",
		},
		{
			DiscID: "disc-one",
			RawURL: "https://images.example.invalid/disc-one.png",
		},
	}

	section := buildDiscScreenshotSections(meta, images, 350, 2, buildScreenshotTag)
	wantOrder := []string{"[b]Disc 1[/b]", "disc-one.png", "[b]Disc 2[/b]", "disc-two.png"}
	previous := -1
	for _, value := range wantOrder {
		index := strings.Index(section, value)
		if index <= previous {
			t.Fatalf("section order for %q in %q", value, section)
		}
		previous = index
	}
}

func TestDVDVOBMediaInfoBlockIncludesEveryDiscOnce(t *testing.T) {
	t.Parallel()

	meta := api.DescriptionSubject{
		DiscType: "DVD",
		Discs: []api.DiscEvidenceResource{
			{
				ID:                  "disc-one",
				Name:                "Disc 1",
				Type:                "DVD",
				DVDVOBMediaInfoText: "VOB INFO ONE",
			},
			{
				ID:                  "disc-two",
				Name:                "Disc 2",
				Type:                "DVD",
				DVDVOBMediaInfoText: "VOB INFO TWO",
			},
		},
		DVDVOBMediaInfoText: "VOB INFO ONE",
	}
	block := DVDVOBMediaInfoBlock(meta)
	if strings.Count(block, "VOB INFO ONE") != 1 || strings.Count(block, "VOB INFO TWO") != 1 {
		t.Fatalf("DVD VOB block = %q", block)
	}
	if strings.Index(block, "Disc 1") >= strings.Index(block, "Disc 2") {
		t.Fatalf("DVD VOB block order = %q", block)
	}
}

func TestBuildDescriptionRemovesOnlyEncodesUploaderBannerFromImportedAndTemplateDescriptions(t *testing.T) {
	t.Parallel()
	for _, banner := range []string{
		"[center]OnlyEncodes Uploader - Powered by L4G's Upload Assistant[/center]",
		"[CENTER]onlyencodes uploader - powered by l4g's upload assistant[/CENTER]",
		"  [center] OnlyEncodes\tUploader  -  Powered by L4G’s Upload Assistant [/center]  ",
		"[right]OnlyEncodes Uploader - Powered by L4G's Upload Assistant[/right]",
		"[align=center]OnlyEncodes Uploader - Powered by L4G's Upload Assistant[/align]",
		"[align=right]OnlyEncodes Uploader - Powered by L4G's Upload Assistant[/align]",
		"[center][b]OnlyEncodes Uploader - Powered by L4G's Upload Assistant[/b][/center]",
		"[center]\r\nOnlyEncodes Uploader - Powered by L4G's Upload Assistant\r\n[/center]",
	} {
		for _, source := range []string{"imported", "template"} {
			t.Run(source+"/"+banner, func(t *testing.T) {
				image := "[img]https://images.example.invalid/screenshot.png[/img]"
				content := "Release notes and original encode credits.\n\n" + banner + "\n\n" + image + "\n\nAdditional release details."
				meta := api.DescriptionSubject{}
				kept := content
				if source == "template" {
					meta.DescriptionTemplate = content
					kept = ""
				}
				got, err := BuildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, kept, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(strings.ToLower(got), "onlyencodes uploader") {
					t.Fatalf("copied banner survived cleanup: %q", got)
				}
				for _, preserved := range []string{"Release notes and original encode credits.", image, "Additional release details."} {
					if !strings.Contains(got, preserved) {
						t.Fatalf("description content lost: %q", preserved)
					}
				}
			})
		}
	}
}

func TestBuildDescriptionPreservesOnlyEncodesProseCreditsAndLiteralBannerExamples(t *testing.T) {
	t.Parallel()
	banner := "[center]OnlyEncodes Uploader - Powered by L4G's Upload Assistant[/center]"
	for _, preserved := range []string{
		"[center]Encode by OnlyEncodes; thanks to the original release team.[/center]",
		"This guide describes the OnlyEncodes Uploader - Powered by L4G's Upload Assistant banner.",
		"[center]OnlyEncodes Uploader - Powered by L4G's Upload Assistant. Additional release credits.[/center]",
		"The literal example " + banner + " is discussed here.",
		"[code]\n" + banner + "\n[/code]",
		"[pre]\n" + banner + "\n[/pre]",
	} {
		for _, source := range []string{"imported", "template"} {
			t.Run(source+"/"+preserved, func(t *testing.T) {
				meta := api.DescriptionSubject{}
				kept := preserved
				if source == "template" {
					meta.DescriptionTemplate = preserved
					kept = ""
				}
				got, err := BuildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, kept, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(got, preserved) {
					t.Fatalf("legitimate content was rewritten: %q", got)
				}
			})
		}
	}
}

func TestBuildDescriptionUsesFullSizeScreenshotsAndPreservesTonemappingNotice(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.Description.TonemappedHeader = "Screenshots are tonemapped."
	got, err := BuildDescription(t.Context(), api.DescriptionSubject{}, cfg, config.TrackerConfig{}, api.NopLogger{},
		"Release notes.\n[center]OnlyEncodes Uploader - Powered by L4G's Upload Assistant[/center]", nil,
		[]api.ScreenshotImage{{
			ImgURL:      "https://images.example.invalid/thumbnail.png",
			RawURL:      "https://images.example.invalid/full.png",
			WebURL:      "https://images.example.invalid/page",
			Tonemapping: api.ScreenshotTonemapped,
		}})
	if err != nil {
		t.Fatal(err)
	}
	expected := "[url=https://images.example.invalid/page][img=350]https://images.example.invalid/full.png[/img][/url]"
	if !strings.Contains(got, expected) || strings.Contains(got, "thumbnail.png") {
		t.Fatalf("full-size embed was lost: %q", got)
	}
	if !strings.Contains(got, "Screenshots are tonemapped.") || !strings.Contains(got, "Release notes.") {
		t.Fatalf("description provenance or content lost: %q", got)
	}
	if strings.Contains(got, "OnlyEncodes Uploader") {
		t.Fatal("copied uploader banner survived")
	}
}

func TestBuildDescriptionSeparatesMenuThumbnailAndFullScreenshotRoles(t *testing.T) {
	t.Parallel()
	menu := api.ScreenshotImage{
		ImgURL: "https://images.example.invalid/menu-thumb.png",
		RawURL: "https://images.example.invalid/menu-full.png",
		WebURL: "https://images.example.invalid/menu-page",
	}
	screen := api.ScreenshotImage{
		ImgURL: "https://images.example.invalid/screen-thumb.png",
		RawURL: "https://images.example.invalid/screen-full.png",
		WebURL: "https://images.example.invalid/screen-page",
	}
	got, err := BuildDescription(
		t.Context(),
		api.DescriptionSubject{},
		config.Config{},
		config.TrackerConfig{},
		api.NopLogger{},
		"Release notes",
		[]api.ScreenshotImage{menu},
		[]api.ScreenshotImage{screen},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[url=" + menu.WebURL + "][img=350]" + menu.ImgURL + "[/img][/url]", "[url=" + screen.WebURL + "][img=350]" + screen.RawURL + "[/img][/url]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrong image role: missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "[img=350]"+menu.RawURL) || strings.Contains(got, screen.ImgURL) {
		t.Fatalf("image roles mixed: %q", got)
	}
}

func TestBuildDescriptionRecognizesKeptThumbnailAndRawImageAliases(t *testing.T) {
	t.Parallel()
	screen := api.ScreenshotImage{
		ImgURL: "https://images.example.invalid/thumb.png",
		RawURL: "https://images.example.invalid/full.png",
		WebURL: "https://images.example.invalid/page",
	}
	for _, existing := range []string{screen.ImgURL, screen.RawURL} {
		t.Run(existing, func(t *testing.T) {
			kept := "Caption: [url=" + screen.WebURL + "][img=300]" + existing + "[/img][/url]"
			got, err := BuildDescription(
				t.Context(),
				api.DescriptionSubject{},
				config.Config{},
				config.TrackerConfig{},
				api.NopLogger{},
				kept,
				nil,
				[]api.ScreenshotImage{screen},
			)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(got, kept) != 1 || strings.Contains(got, "[img=350]"+screen.RawURL) {
				t.Fatalf("kept image alias duplicated or rewritten: %q", got)
			}
		})
	}
}

func TestBuildDescriptionDedupesMenuImageAliasesButNotProseOrPageLinks(t *testing.T) {
	t.Parallel()
	menu := api.ScreenshotImage{
		ImgURL: "https://images.example.invalid/menu-thumb.png",
		RawURL: "https://images.example.invalid/menu-full.png",
		WebURL: "https://images.example.invalid/shared-page",
	}
	screen := api.ScreenshotImage{
		ImgURL: "https://images.example.invalid/screen-thumb.png",
		RawURL: "https://images.example.invalid/screen-full.png",
		WebURL: menu.WebURL,
	}
	kept := "Reference: [url=" + screen.WebURL + "]page[/url] and " + screen.RawURL
	got, err := BuildDescription(
		t.Context(),
		api.DescriptionSubject{},
		config.Config{},
		config.TrackerConfig{},
		api.NopLogger{},
		kept,
		[]api.ScreenshotImage{menu},
		[]api.ScreenshotImage{screen, {ImgURL: menu.RawURL}, {RawURL: menu.ImgURL}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "[img=350]"+screen.RawURL+"[/img]") {
		t.Fatalf("prose/page URL suppressed unrelated screenshot: %q", got)
	}
	if strings.Count(got, "[img=350]"+menu.ImgURL+"[/img]") != 1 || strings.Contains(got, "[img=350]"+menu.RawURL+"[/img]") {
		t.Fatalf("menu aliases duplicated as screenshots: %q", got)
	}
}
