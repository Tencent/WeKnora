package sandbox

import "testing"

func TestIsStandardTemplateRecognizesProviderScopedName(t *testing.T) {
	for _, name := range []string{"weknora", "team/weknora", "project-b89e/WeKnora"} {
		if !isStandardTemplate(name) {
			t.Fatalf("expected %q to identify the WeKnora standard template", name)
		}
	}
	if isStandardTemplate("weknora-custom") {
		t.Fatal("custom template must not be treated as the standard template")
	}
	if isStandardTemplate(DesktopTemplateName) {
		t.Fatal("the desktop sibling must not be classified as the CLI standard template")
	}
}

func TestIsDesktopTemplateRecognizesProviderScopedName(t *testing.T) {
	for _, name := range []string{"weknora-desktop", "team/weknora-desktop", "project-b89e/WeKnora-Desktop"} {
		if !isDesktopTemplate(name) {
			t.Fatalf("expected %q to identify the WeKnora desktop template", name)
		}
	}
	if isDesktopTemplate("weknora") {
		t.Fatal("the CLI template must not be classified as desktop")
	}
	if isDesktopTemplate("weknora-desktop-custom") {
		t.Fatal("a similarly prefixed custom name must not be the desktop template")
	}
}

func TestClassifyWeKnoraTemplatePrefersNameOverImage(t *testing.T) {
	standard, desktop := classifyWeKnoraTemplate(DesktopTemplateName, DefaultDockerImage)
	if standard || !desktop {
		t.Fatalf(
			"named desktop template must be desktop even if the image repo matches CLI, got standard=%v desktop=%v",
			standard, desktop,
		)
	}
	standard, desktop = classifyWeKnoraTemplate(StandardTemplateName, DefaultDesktopDockerImage)
	if !standard || desktop {
		t.Fatalf(
			"named CLI template must stay CLI even if the image tag is desktop, got standard=%v desktop=%v",
			standard, desktop,
		)
	}
}

func TestClassifyWeKnoraTemplateNamelessImageUsesTag(t *testing.T) {
	standard, desktop := classifyWeKnoraTemplate("", DefaultDockerImage)
	if !standard || desktop {
		t.Fatalf("CLI image with no name must be standard, got standard=%v desktop=%v", standard, desktop)
	}
	standard, desktop = classifyWeKnoraTemplate("", DefaultDesktopDockerImage)
	if standard || !desktop {
		t.Fatalf("desktop image with no name must be desktop, got standard=%v desktop=%v", standard, desktop)
	}
	standard, desktop = classifyWeKnoraTemplate("", DefaultCubeDesktopTemplateImage)
	if standard || !desktop {
		t.Fatalf("Cube desktop image with no name must be desktop, got standard=%v desktop=%v", standard, desktop)
	}
}
