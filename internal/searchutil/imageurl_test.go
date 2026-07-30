package searchutil

import "testing"

func TestCanonicalizeImageURLsForModel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "markdown local export keeps alt text",
			in:   `before ![Figure 2: Architecture](local://10000/exports/random-a.jpg) after`,
			want: `before ![Figure 2: Architecture]([image]) after`,
		},
		{
			name: "markdown signed URL keeps title",
			in:   `![plot](https://objects.example/uuid.png?X-Signature=one "training curve")`,
			want: `![plot]([image] "training curve")`,
		},
		{
			name: "markdown angle destination",
			in:   `![scan](<minio://bucket/path with spaces/page.png>)`,
			want: `![scan]([image])`,
		},
		{
			name: "html image keeps semantic attributes",
			in:   `<img alt="confusion matrix" src="provider://exports/random.png" width="200">`,
			want: `<img alt="confusion matrix" src="[image]" width="200">`,
		},
		{
			name: "enriched image keeps caption body",
			in:   `<image url='local://exports/random.jpg'><image_caption>two people</image_caption></image>`,
			want: `<image url='[image]'><image_caption>two people</image_caption></image>`,
		},
		{
			name: "ordinary link remains intact",
			in:   `[paper](https://example.com/paper.pdf)`,
			want: `[paper](https://example.com/paper.pdf)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanonicalizeImageURLsForModel(tt.in); got != tt.want {
				t.Fatalf("CanonicalizeImageURLsForModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanonicalizeImageURLsForModelIsStableAcrossExportURLs(t *testing.T) {
	first := `![Figure 3](local://10000/exports/uuid-a_111.jpg)`
	second := `![Figure 3](https://minio.example/signed/uuid-b.jpg?signature=222)`
	if gotFirst, gotSecond := CanonicalizeImageURLsForModel(first), CanonicalizeImageURLsForModel(second); gotFirst != gotSecond {
		t.Fatalf("canonical inputs differ: %q != %q", gotFirst, gotSecond)
	}

	differentCaption := `![Figure 4](local://10000/exports/uuid-a_111.jpg)`
	if CanonicalizeImageURLsForModel(first) == CanonicalizeImageURLsForModel(differentCaption) {
		t.Fatal("different alt text must remain distinguishable")
	}
}
