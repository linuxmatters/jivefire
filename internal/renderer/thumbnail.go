package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

// getThumbnailTextColor returns the brand yellow color for thumbnail text
func getThumbnailTextColor() color.RGBA {
	return color.RGBA{R: config.TextColorR, G: config.TextColorG, B: config.TextColorB, A: 255}
}

// GenerateThumbnail creates a YouTube thumbnail with the title text overlaid
// The thumbnail is the same resolution as the video (1280x720)
func GenerateThumbnail(outputPath string, title string) error {
	// Load the thumbnail background image
	thumbImg, err := loadThumbnailBackground()
	if err != nil {
		return fmt.Errorf("failed to load thumbnail background: %w", err)
	}

	// Load the bold font for thumbnail
	fontData, err := embeddedAssets.ReadFile(config.ThumbnailFontAsset)
	if err != nil {
		return fmt.Errorf("failed to load bold font: %w", err)
	}

	parsedFont, err := truetype.Parse(fontData)
	if err != nil {
		return fmt.Errorf("failed to parse font: %w", err)
	}

	// Split title into 2 lines
	line1, line2 := splitTitle(title)

	// Find the largest font size that fits within constraints
	fontSize := findOptimalFontSize(parsedFont, line1, line2)

	// Create font face with optimal size
	face := truetype.NewFace(parsedFont, &truetype.Options{
		Size: fontSize,
		DPI:  72,
	})
	defer face.Close()

	// Draw the text on the thumbnail
	drawThumbnailText(thumbImg, face, line1, line2)

	// Save the thumbnail
	if err := saveThumbnail(thumbImg, outputPath); err != nil {
		return fmt.Errorf("failed to save thumbnail: %w", err)
	}

	return nil
}

// loadThumbnailBackground loads and scales the embedded thumbnail background
func loadThumbnailBackground() (*image.RGBA, error) {
	data, err := embeddedAssets.ReadFile(config.ThumbnailImageAsset)
	if err != nil {
		return nil, err
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	// Check if scaling is needed
	bounds := img.Bounds()
	if bounds.Dx() == config.Width && bounds.Dy() == config.Height {
		// Already correct size, just convert to RGBA
		rgba := image.NewRGBA(bounds)
		draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
		return rgba, nil
	}

	// Scale to video resolution using the same method as LoadBackgroundImage
	dst := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Src, nil)
	return dst, nil
}

// splitTitle splits the title into 2 roughly equal lines
func splitTitle(title string) (string, string) {
	words := strings.Fields(title)
	if len(words) == 0 {
		return "", ""
	}
	if len(words) == 1 {
		return words[0], ""
	}

	// Split at the midpoint for roughly equal lines
	mid := len(words) / 2
	line1 := strings.Join(words[:mid], " ")
	line2 := strings.Join(words[mid:], " ")

	return line1, line2
}

// findOptimalFontSize finds the largest font size that fits within constraints
// Constraints:
// - ThumbnailMargin from left and right edges
// - Line 1 starts at top margin (ThumbnailMargin)
// - Bottom edge of line 2 must not extend below center line (y=360)
func findOptimalFontSize(parsedFont *truetype.Font, line1, line2 string) float64 {
	centerY := config.Height / 2
	maxWidth := config.Width - (2 * config.ThumbnailMargin)

	// Start with a large size and reduce until it fits
	for size := 150.0; size > 10.0; size -= 2.0 {
		face := truetype.NewFace(parsedFont, &truetype.Options{
			Size: size,
			DPI:  72,
		})

		// Measure both lines
		width1, bounds1 := measureText(face, line1)
		width2, bounds2 := measureText(face, line2)

		face.Close()

		// Check if both lines fit within width constraint
		if width1 > maxWidth || width2 > maxWidth {
			continue
		}

		// Calculate line spacing (50% of font size for more vertical spacing)
		lineSpacing := int(size * 0.5)

		// Height of each line (from top to bottom of glyphs)
		height1 := (bounds1.Max.Y - bounds1.Min.Y).Ceil()
		height2 := (bounds2.Max.Y - bounds2.Min.Y).Ceil()

		// Calculate where line 2 bottom would be:
		// Line 1 top: margin
		// Line 1 bottom: margin + height1
		// Line 2 top: margin + height1 + lineSpacing
		// Line 2 bottom: margin + height1 + lineSpacing + height2
		line2Bottom := config.ThumbnailMargin + height1 + lineSpacing + height2

		// Check if line 2 bottom fits above center line
		if line2Bottom <= centerY {
			return size
		}
	}

	return 10.0 // Minimum fallback size
}

// measureText returns the width and actual bounds of rendered text
// Returns width, and the bounds rectangle (Min.Y is negative for ascent, Max.Y is positive for descent)
func measureText(face font.Face, text string) (int, fixed.Rectangle26_6) {
	d := &font.Drawer{Face: face}
	bounds, _ := d.BoundString(text)
	width := (bounds.Max.X - bounds.Min.X).Ceil()
	return width, bounds
}

// drawThumbnailText draws the title text on the thumbnail with a slight rotation
// Line 1 is top-aligned at the ThumbnailMargin
// Bottom edge of line 2 must not extend below center line
// Text is rotated ThumbnailTextRotationDegrees clockwise for dynamic effect
func drawThumbnailText(img *image.RGBA, face font.Face, line1, line2 string) {
	// Measure text dimensions - bounds.Min.Y is negative (ascent), bounds.Max.Y is positive (descent)
	width1, bounds1 := measureText(face, line1)
	width2, bounds2 := measureText(face, line2)

	// Calculate line spacing (50% of font size for more vertical spacing)
	metrics := face.Metrics()
	fontSize := float64(metrics.Height) / 64.0 // Convert from fixed.Int26_6 to float64
	lineSpacing := int(fontSize * 0.5)

	// Calculate the height of each line (from visual top to visual bottom)
	height1 := (bounds1.Max.Y - bounds1.Min.Y).Ceil()
	height2 := (bounds2.Max.Y - bounds2.Min.Y).Ceil()

	// Calculate total text block dimensions
	maxWidth := width1
	if width2 > maxWidth {
		maxWidth = width2
	}
	totalHeight := height1 + lineSpacing + height2

	// Create a temporary image for drawing text (larger to accommodate rotation)
	// Use 1.5x size to ensure no clipping during rotation
	tempSize := int(float64(maxWidth+totalHeight) * 1.5)
	tempImg := image.NewRGBA(image.Rect(0, 0, tempSize, tempSize))

	// Draw text on temporary image, centered
	// The baseline is where DrawString draws. The visual top is baseline + bounds.Min.Y (Min.Y is negative)
	tempCenterY := tempSize / 2

	// Position line 1: we want the text block centered, so calculate baseline positions
	// Text block spans from (center - totalHeight/2) to (center + totalHeight/2)
	// Line 1's visual top should be at (center - totalHeight/2)
	// Since visual top = baseline + bounds1.Min.Y, we get: baseline = visualTop - bounds1.Min.Y
	line1VisualTop := tempCenterY - totalHeight/2
	line1BaselineY := line1VisualTop - bounds1.Min.Y.Ceil() // Min.Y is negative, so this adds to visualTop

	// Line 2's visual top is line1VisualTop + height1 + lineSpacing
	line2VisualTop := line1VisualTop + height1 + lineSpacing
	line2BaselineY := line2VisualTop - bounds2.Min.Y.Ceil()

	drawCenteredLineOnTemp(tempImg, face, line1, tempSize, line1BaselineY)
	drawCenteredLineOnTemp(tempImg, face, line2, tempSize, line2BaselineY)

	// Create rotation matrix for thumbnail text rotation (clockwise)
	angle := -config.ThumbnailTextRotationDegrees * math.Pi / 180.0 // Negative for clockwise
	cos := math.Cos(angle)
	sin := math.Sin(angle)

	// Center of rotation (center of temp image)
	cx := float64(tempSize) / 2.0
	cy := float64(tempSize) / 2.0

	// Create affine transformation matrix
	// Translate to origin, rotate, translate back
	m := f64.Aff3{
		cos, -sin, cx - cos*cx + sin*cy,
		sin, cos, cy - sin*cx - cos*cy,
	}

	// Create destination image for rotated text
	rotatedImg := image.NewRGBA(tempImg.Bounds())

	// Apply rotation
	draw.BiLinear.Transform(rotatedImg, m, tempImg, tempImg.Bounds(), draw.Over, nil)

	// Calculate the position to composite rotated text onto thumbnail
	// For a clockwise rotation, the highest point will be the top-right corner of line 1

	// Line 1's visual top in tempImg coordinates
	line1Top := float64(line1VisualTop)

	// Get the right edge of line 1
	line1Right := cx + float64(width1)/2.0

	// This is the point that will be highest after clockwise rotation
	topRightX := line1Right - cx // Relative to rotation center
	topRightY := line1Top - cy   // Relative to rotation center

	// Apply rotation to find where this point ends up
	// For clockwise rotation: y' = x*sin + y*cos
	rotatedTopY := sin*topRightX + cos*topRightY

	// Translate back to tempImg coordinates
	highestPointY := rotatedTopY + cy

	// Position the rotated text block centered horizontally
	destX := (config.Width - tempSize) / 2

	// For vertical positioning:
	// highestPointY is the highest point of the rotated text in tempImg coordinates
	// We want this highest point to align with config.ThumbnailMargin in the final image
	// So: destY + highestPointY = config.ThumbnailMargin
	// Therefore: destY = config.ThumbnailMargin - highestPointY
	destY := int(float64(config.ThumbnailMargin) - highestPointY)

	// Composite rotated text onto thumbnail
	destRect := image.Rect(destX, destY, destX+tempSize, destY+tempSize)
	draw.Draw(img, destRect, rotatedImg, image.Point{}, draw.Over)
}

// drawCenteredLineOnTemp draws a line of text centered on a temporary image
func drawCenteredLineOnTemp(img *image.RGBA, face font.Face, text string, imgWidth, baselineY int) {
	if text == "" {
		return
	}

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(getThumbnailTextColor()),
		Face: face,
	}

	// Measure text width
	bounds, _ := d.BoundString(text)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()

	// Center horizontally
	x := (imgWidth - textWidth) / 2

	d.Dot = freetype.Pt(x, baselineY)
	d.DrawString(text)
}

// saveThumbnail saves the thumbnail image to a PNG file
func saveThumbnail(img *image.RGBA, outputPath string) error {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	return png.Encode(outFile, img)
}
