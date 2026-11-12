package main

import (
	"testing"

	"arx/arx_go/models"
)

// Characterization (#446): pins down today's behavior that every non-empty
// pf_type="attach" result is treated as an image by isImageRow, regardless of
// file type — proves the PDF carve-out added for #446 (isFileRow) is
// additive to isImageRow's existing image-type behavior, not a regression.
func TestIsImageRow_AttachStepAnyValueIsImage(t *testing.T) {
	row := models.ResultRow{
		Step:   &models.TestStep{PFType: "attach"},
		Result: &models.TestResult{Result: "shot.png"},
	}
	if !isImageRow(row) {
		t.Errorf("isImageRow(attach step, non-PDF value) = false, want true")
	}
}

// Red (#446): a PDF attach-step result must be excluded from the image
// gallery (isImageRow) and picked up instead by isFileRow. Fails today:
// isFileRow does not exist and isImageRow treats every attach value,
// including a PDF, as an image.
func TestIsFileRow_PDFAttachStepIsFileNotImage(t *testing.T) {
	row := models.ResultRow{
		Step:   &models.TestStep{PFType: "attach"},
		Result: &models.TestResult{Result: "cert.pdf"},
	}
	if isImageRow(row) {
		t.Errorf("isImageRow(attach step, PDF value) = true, want false")
	}
	if !isFileRow(row) {
		t.Errorf("isFileRow(attach step, PDF value) = false, want true")
	}
}
