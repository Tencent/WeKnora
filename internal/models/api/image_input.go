package api

import (
	"fmt"
	"slices"
)

// ImageInput is how an endpoint takes images, shared by the embedding and
// rerank settings. Its fields are flattened into both, so the compat keys
// read the same on either: image_field, max_image_batch_size,
// max_image_bytes and image_mime_types.
type ImageInput struct {
	// ImageField is the key of the object that carries an image on a shape
	// with no standard image input ("image" on Jina). Protocols whose schema
	// fixes the image part ignore it.
	ImageField string `json:",omitempty"`
	// MaxImageBatchSize is the documented number of images per request.
	// 0 means one: a vendor that fuses its inputs, or does not say, must not
	// be sent several.
	MaxImageBatchSize int `json:",omitempty"`
	// MaxImageBytes is the documented size limit of one image, 0 when the
	// vendor states none. The caller shrinks an image to fit; the client
	// refuses one that does not rather than spend a request on it.
	MaxImageBytes int `json:",omitempty"`
	// ImageMIMETypes lists the formats the vendor documents, empty when it
	// takes any common one.
	ImageMIMETypes []string `json:",omitempty"`
}

// ImageBatchLimit is the number of images one request may carry.
func (s ImageInput) ImageBatchLimit() int {
	if s.MaxImageBatchSize > 0 {
		return s.MaxImageBatchSize
	}
	return 1
}

// CheckImage reports an image the vendor documents it will not take.
func (s ImageInput) CheckImage(img EmbedImage) error {
	if len(img.Data) == 0 {
		return fmt.Errorf("image is empty")
	}
	if s.MaxImageBytes > 0 && len(img.Data) > s.MaxImageBytes {
		return fmt.Errorf("image is %d bytes; the limit is %d", len(img.Data), s.MaxImageBytes)
	}
	if len(s.ImageMIMETypes) > 0 && !slices.Contains(s.ImageMIMETypes, img.MIMEType) {
		return fmt.Errorf("image type %q is not one of %v", img.MIMEType, s.ImageMIMETypes)
	}
	return nil
}
