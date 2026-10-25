package main

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
)

type variantSpec struct {
	name        string
	maxWidth    int
	maxHeight   int
	exactSquare bool
}

var requiredVariants = []variantSpec{
	{name: "thumbnail", maxWidth: 150, maxHeight: 150, exactSquare: true},
	{name: "preview", maxWidth: 800, maxHeight: 600, exactSquare: false},
	{name: "display", maxWidth: 1200, maxHeight: 900, exactSquare: false},
}

// generateVariant takes an image and a variantSpec, and returns a new image that is resized according to the spec.
func generateVariant(img image.Image, spec variantSpec) image.Image {
	// Resize the image according to the spec
	if spec.exactSquare {
		// Resize to exact square dimensions
		return resizeToExactSquare(img, spec.maxWidth)
	}
	return resizeToFit(img, spec.maxWidth, spec.maxHeight)
}

// resizeToExactSquare resizes the image to the exact square dimensions specified.
func resizeToExactSquare(img image.Image, size int) image.Image {
	// Implement resizing logic to exact square dimensions
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Determine the smaller dimension to maintain aspect ratio
	var newSize int
	if width < height {
		newSize = width
	} else {
		newSize = height
	}

	// Calculate the offset to center the crop
	offsetX := (width - newSize) / 2
	offsetY := (height - newSize) / 2

	// Crop the image to a square
	croppedImg := img.(interface {
		SubImage(r image.Rectangle) image.Image
	}).SubImage(image.Rect(offsetX, offsetY, offsetX+newSize, offsetY+newSize))

	// Resize the cropped image to the specified size
	return resizeImage(croppedImg, size, size)
}

func resizeToFit(img image.Image, maxWidth, maxHeight int) image.Image {
	// Implement resizing logic to fit within the specified dimensions while maintaining aspect ratio
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Calculate the scaling factor
	scaleX := float64(maxWidth) / float64(width)
	scaleY := float64(maxHeight) / float64(height)
	scale := scaleX
	if scaleY < scaleX {
		scale = scaleY
	}

	// Calculate new dimensions
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)

	if newWidth == width && newHeight == height {
		// No resizing needed
		return img
	}

	// Resize the image to the new dimensions
	return resizeImage(img, newWidth, newHeight)
}

// resizeImage resizes the given image to the specified width and height using nearest-neighbor scaling.
func resizeImage(img image.Image, width, height int) image.Image {
	bounds := img.Bounds()
	// Create a new RGBA image with the specified dimensions
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	// Simple nearest-neighbor scaling
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX := bounds.Min.X + x*bounds.Dx()/width
			srcY := bounds.Min.Y + y*bounds.Dy()/height
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}

	return dst
}
