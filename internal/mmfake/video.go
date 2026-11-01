package mmfake

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
)

// demoAVI — ролик для сидов и тестов: AVI с кадрами MJPEG, без звука. По
// кадру ползёт полоса, фон меняет цвет — видно, что видео идёт.
func demoAVI(w, h, frames, fps int) []byte {
	var jpegs [][]byte
	for i := range frames {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		bg := color.RGBA{uint8(40 + 160*i/frames), 90, uint8(200 - 120*i/frames), 255}
		bar := w * (i + 1) / frames
		for y := range h {
			for x := range w {
				c := bg
				if y > h*3/4 && x < bar {
					c = color.RGBA{0xfc, 0xfb, 0xf9, 255}
				}
				img.Set(x, y, c)
			}
		}
		var b bytes.Buffer
		jpeg.Encode(&b, img, &jpeg.Options{Quality: 70})
		jpegs = append(jpegs, b.Bytes())
	}
	return aviMJPEG(w, h, fps, jpegs)
}

// aviMJPEG собирает RIFF AVI: заголовки (avih, strh, strf), кадры «00dc» и индекс idx1.
func aviMJPEG(w, h, fps int, frames [][]byte) []byte {
	le := binary.LittleEndian
	u32 := func(b *bytes.Buffer, v int) { binary.Write(b, le, uint32(v)) }
	u16 := func(b *bytes.Buffer, v int) { binary.Write(b, le, uint16(v)) }
	chunk := func(id string, data []byte) []byte {
		var b bytes.Buffer
		b.WriteString(id)
		u32(&b, len(data))
		b.Write(data)
		if len(data)%2 == 1 {
			b.WriteByte(0)
		}
		return b.Bytes()
	}
	list := func(kind string, parts ...[]byte) []byte {
		var b bytes.Buffer
		b.WriteString(kind)
		for _, p := range parts {
			b.Write(p)
		}
		return chunk("LIST", b.Bytes())
	}
	maxFrame := 0
	for _, f := range frames {
		maxFrame = max(maxFrame, len(f))
	}

	var avih bytes.Buffer
	u32(&avih, 1000000/fps) // мкс на кадр
	u32(&avih, maxFrame*fps)
	u32(&avih, 0)
	u32(&avih, 0x10) // AVIF_HASINDEX
	u32(&avih, len(frames))
	u32(&avih, 0)
	u32(&avih, 1) // потоков
	u32(&avih, maxFrame)
	u32(&avih, w)
	u32(&avih, h)
	avih.Write(make([]byte, 16))

	var strh bytes.Buffer
	strh.WriteString("vids")
	strh.WriteString("MJPG")
	u32(&strh, 0)
	u16(&strh, 0)
	u16(&strh, 0)
	u32(&strh, 0)
	u32(&strh, 1)   // scale
	u32(&strh, fps) // rate
	u32(&strh, 0)
	u32(&strh, len(frames))
	u32(&strh, maxFrame)
	u32(&strh, 0xffffffff)
	u32(&strh, 0)
	u16(&strh, 0)
	u16(&strh, 0)
	u16(&strh, w)
	u16(&strh, h)

	var strf bytes.Buffer // BITMAPINFOHEADER
	u32(&strf, 40)
	u32(&strf, w)
	u32(&strf, h)
	u16(&strf, 1)
	u16(&strf, 24)
	strf.WriteString("MJPG")
	u32(&strf, w*h*3)
	u32(&strf, 0)
	u32(&strf, 0)
	u32(&strf, 0)
	u32(&strf, 0)

	hdrl := list("hdrl", chunk("avih", avih.Bytes()), list("strl", chunk("strh", strh.Bytes()), chunk("strf", strf.Bytes())))

	var movi, idx bytes.Buffer
	movi.WriteString("movi")
	for _, f := range frames {
		off := movi.Len()
		movi.Write(chunk("00dc", f))
		idx.WriteString("00dc")
		u32(&idx, 0x10) // AVIIF_KEYFRAME
		u32(&idx, off)
		u32(&idx, len(f))
	}

	var body bytes.Buffer
	body.WriteString("AVI ")
	body.Write(hdrl)
	body.Write(chunk("LIST", movi.Bytes()))
	body.Write(chunk("idx1", idx.Bytes()))
	return chunk("RIFF", body.Bytes())
}
