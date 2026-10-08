//go:build anydoc && cgo

package docparser

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"path"
	"sort"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/types"
)

// TestEmbeddedVectorImageLinksResolve guards the string contract between
// anydoc's Rust serializer and this package's image resolver.
//
// anydoc renders an embedded image in place as `![alt](images/image-N<ext>)`,
// where `<ext>` comes from `asset_links.rs::extension_for(asset.media_type)`.
// ImageResolver looks that exact string up in a map keyed by
// `anydoc.ImageDir + Asset.Name`, and `Asset.Name` is built from
// `backend_cgo.go::extensionFor(asset.MediaType)`. Nothing normalises the two,
// so any disagreement about the extension makes the lookup miss and the image
// bytes are never stored, silently.
//
// EMF and WMF are what Word, Excel and PowerPoint use for pasted charts,
// equations and Visio drawings. The two sides used to disagree about them: the
// Rust table fell through to ".bin" while the Go side asked the platform MIME
// registry and got ".emf"/".wmf". This test converts a document carrying one of
// each and requires every Markdown image reference to resolve.
func TestEmbeddedVectorImageLinksResolve(t *testing.T) {
	result, err := anydoc.Convert(vectorDocx(t), anydoc.Options{Format: "docx", WithAssets: true})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.AssetsError != nil {
		t.Fatalf("image extraction failed: %v", result.AssetsError)
	}

	var mediaTypes []string
	for _, asset := range result.Assets {
		mediaTypes = append(mediaTypes, asset.MediaType)
	}
	sort.Strings(mediaTypes)
	if want := []string{"image/emf", "image/wmf"}; !equalStrings(mediaTypes, want) {
		t.Fatalf("extracted media types = %v, want %v", mediaTypes, want)
	}

	// Exactly what ImageResolver.ResolveAndStore builds before looking up the
	// Markdown reference.
	refMap := map[string]types.ImageRef{}
	for _, ref := range imageRefsFromAssets(result.Assets) {
		refMap[ref.OriginalRef] = ref
	}

	references := scanMarkdownImageTargets(result.Markdown)
	if len(references) != len(result.Assets) {
		t.Fatalf("markdown carries %d image references, want %d:\n%s",
			len(references), len(result.Assets), result.Markdown)
	}
	for _, reference := range references {
		raw := result.Markdown[reference.TargetStart:reference.TargetEnd]
		if _, _, _, ok := splitMarkdownImageTarget(raw, refMap); !ok {
			t.Errorf("markdown reference %q matches no ImageRef, so the image would never be stored (lookup keys: %v)",
				raw, sortedRefKeys(refMap))
		}
	}

	// Agreeing on ".bin" for everything would satisfy the loop above while
	// still discarding the format, so pin the extensions too. Asset order
	// follows the archive rather than the document, so compare the set.
	var extensions []string
	for key := range refMap {
		extensions = append(extensions, path.Ext(key))
	}
	sort.Strings(extensions)
	if want := []string{".emf", ".wmf"}; !equalStrings(extensions, want) {
		t.Errorf("image reference extensions = %v, want %v (lookup keys: %v)",
			extensions, want, sortedRefKeys(refMap))
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sortedRefKeys(refMap map[string]types.ImageRef) []string {
	keys := make([]string, 0, len(refMap))
	for key := range refMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// vectorDocx builds a minimal .docx holding one EMF and one WMF drawing. The
// payloads only need a recognisable header: anydoc addresses an embedded part
// through its relationship target and extension, not by decoding the image.
func vectorDocx(t *testing.T) []byte {
	t.Helper()

	const (
		relsNS = "http://schemas.openxmlformats.org/package/2006/relationships"
		wNS    = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
		rNS    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
		aNS    = "http://schemas.openxmlformats.org/drawingml/2006/main"
		wpNS   = "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
		picNS  = "http://schemas.openxmlformats.org/drawingml/2006/picture"
	)

	files := []struct {
		name string
		body string
	}{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="emf" ContentType="image/x-emf"/>
<Default Extension="wmf" ContentType="image/x-wmf"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="` + relsNS + `"><Relationship Id="rId1" Type="` + rNS + `/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="` + relsNS + `">
<Relationship Id="rId1" Type="` + rNS + `/image" Target="media/image1.emf"/>
<Relationship Id="rId2" Type="` + rNS + `/image" Target="media/image2.wmf"/>
</Relationships>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="` + wNS + `" xmlns:r="` + rNS + `" xmlns:a="` + aNS + `" xmlns:wp="` + wpNS + `">
<w:body><w:p><w:r><w:t>vector images</w:t></w:r></w:p>` +
			drawingParagraph(picNS, aNS, wpNS, rNS, "rId1", "chart.emf", 1) +
			drawingParagraph(picNS, aNS, wpNS, rNS, "rId2", "equation.wmf", 2) +
			`</w:body></w:document>`},
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range files {
		writer, err := zw.Create(file.name)
		if err != nil {
			t.Fatalf("zip create %s: %v", file.name, err)
		}
		if _, err := writer.Write([]byte(file.body)); err != nil {
			t.Fatalf("zip write %s: %v", file.name, err)
		}
	}
	for name, body := range map[string][]byte{
		"word/media/image1.emf": emfHeader(),
		"word/media/image2.wmf": wmfPlaceableHeader(),
	} {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func drawingParagraph(picNS, aNS, wpNS, rNS, relID, name string, id int) string {
	number := strconv.Itoa(id)
	return `<w:p><w:r><w:drawing><wp:inline>
<wp:extent cx="914400" cy="914400"/>
<wp:docPr id="` + number + `" name="` + name + `" descr="` + name + `"/>
<a:graphic><a:graphicData uri="` + picNS + `">
<pic:pic xmlns:pic="` + picNS + `">
<pic:nvPicPr><pic:cNvPr id="` + number + `" name="` + name + `"/><pic:cNvPicPr/></pic:nvPicPr>
<pic:blipFill><a:blip r:embed="` + relID + `"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>
<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="914400" cy="914400"/></a:xfrm>
<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>
</pic:pic></a:graphicData></a:graphic>
</wp:inline></w:drawing></w:r></w:p>`
}

// emfHeader is a structurally valid EMR_HEADER record (MS-EMF 2.3.4.2).
func emfHeader() []byte {
	buf := make([]byte, 0, 88)
	buf = binary.LittleEndian.AppendUint32(buf, 1) // iType = EMR_HEADER
	buf = binary.LittleEndian.AppendUint32(buf, 88)
	for i := 0; i < 8; i++ { // rclBounds, rclFrame
		buf = binary.LittleEndian.AppendUint32(buf, 914400)
	}
	buf = binary.LittleEndian.AppendUint32(buf, 0x464D4520) // dSignature = ' EMF'
	buf = binary.LittleEndian.AppendUint32(buf, 0x00010000) // nVersion
	buf = binary.LittleEndian.AppendUint32(buf, 88)         // nBytes
	buf = binary.LittleEndian.AppendUint32(buf, 16)         // nRecords
	buf = binary.LittleEndian.AppendUint16(buf, 0)          // nHandles
	buf = binary.LittleEndian.AppendUint16(buf, 0)          // sReserved
	for i := 0; i < 3; i++ {                                // nDescription, offDescription, nPalEntries
		buf = binary.LittleEndian.AppendUint32(buf, 0)
	}
	for i := 0; i < 4; i++ { // szlDevice, szlMillimeters
		buf = binary.LittleEndian.AppendUint32(buf, 0)
	}
	return append(buf, []byte("EMF-PAYLOAD")...)
}

// wmfPlaceableHeader is a placeable WMF header (MS-WMF 2.3.2.1).
func wmfPlaceableHeader() []byte {
	buf := make([]byte, 0, 22)
	buf = binary.LittleEndian.AppendUint32(buf, 0x9AC6CDD7) // key
	buf = binary.LittleEndian.AppendUint16(buf, 0)          // hmf
	for i := 0; i < 4; i++ {                                // bbox
		buf = binary.LittleEndian.AppendUint16(buf, 100)
	}
	buf = binary.LittleEndian.AppendUint16(buf, 96) // inch
	buf = binary.LittleEndian.AppendUint32(buf, 0)  // reserved
	buf = binary.LittleEndian.AppendUint16(buf, 96) // checksum
	return append(buf, []byte("WMF-PAYLOAD")...)
}
