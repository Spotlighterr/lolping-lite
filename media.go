//go:build windows

package main

import (
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"
)

type frameEntry struct {
	endMS  uint32
	offset int64
	size   int64
}
type emoteAnimation struct {
	file    io.ReadSeekCloser
	frames  []frameEntry
	image   *premultImg
	current int
}

func openEmote(path string) *emoteAnimation {
	exe, _ := os.Executable()
	f, err := os.Open(filepath.Join(filepath.Dir(exe), filepath.FromSlash(path)))
	if err != nil {
		f, err = os.Open(path)
	}
	if err != nil {
		return nil
	}
	var file io.ReadSeekCloser = f
	var header [12]byte
	if _, err = io.ReadFull(file, header[:]); err != nil {
		file.Close()
		return nil
	}
	a := &emoteAnimation{file: file, current: -1, image: &premultImg{w: int(binary.LittleEndian.Uint32(header[:4])), h: int(binary.LittleEndian.Uint32(header[4:8]))}}
	a.image.pix = make([]byte, a.image.w*a.image.h*4)
	for i := uint32(0); i < binary.LittleEndian.Uint32(header[8:]); i++ {
		var entry [8]byte
		if _, err = io.ReadFull(file, entry[:]); err != nil {
			a.close()
			return nil
		}
		offset, _ := file.Seek(0, io.SeekCurrent)
		size := int64(binary.LittleEndian.Uint32(entry[4:]))
		a.frames = append(a.frames, frameEntry{binary.LittleEndian.Uint32(entry[:4]), offset, size})
		file.Seek(size, io.SeekCurrent)
	}
	return a
}
func (a *emoteAnimation) at(seconds float64) *premultImg {
	idx := len(a.frames) - 1
	for i, frame := range a.frames {
		if uint32(seconds*1000) < frame.endMS {
			idx = i
			break
		}
	}
	if idx != a.current && idx >= 0 {
		f := a.frames[idx]
		a.file.Seek(f.offset, io.SeekStart)
		reader, err := zlib.NewReader(io.LimitReader(a.file, f.size))
		if err == nil {
			_, err = io.ReadFull(reader, a.image.pix)
			reader.Close()
		}
		if err != nil {
			clear(a.image.pix)
		}
		a.current = idx
	}
	return a.image
}
func (a *emoteAnimation) close() { a.file.Close() }

type waveFormat struct {
	Tag, Channels           uint16
	Rate, BytesPerSec       uint32
	BlockAlign, Bits, Extra uint16
}
type waveHeader struct {
	Data             *byte
	Length, Recorded uint32
	User             uintptr
	Flags, Loops     uint32
	Next, Reserved   uintptr
}
type soundVoice struct {
	handle uintptr
	header *waveHeader
	data   []byte
}

type cachedWav struct {
	format waveFormat
	pcm    []byte
}

var (
	voiceMu     sync.Mutex
	voices      []*soundVoice
	pcmCacheMu  sync.Mutex
	pcmCache    = map[string]cachedWav{}
	activeAudio int
)

var (
	pWaveOutOpen            = winmm.NewProc("waveOutOpen")
	pWaveOutPrepareHeader   = winmm.NewProc("waveOutPrepareHeader")
	pWaveOutWrite           = winmm.NewProc("waveOutWrite")
	pWaveOutUnprepareHeader = winmm.NewProc("waveOutUnprepareHeader")
	pWaveOutReset           = winmm.NewProc("waveOutReset")
	pWaveOutClose           = winmm.NewProc("waveOutClose")
)

func reapSounds() {}

func stopSounds() {
	voiceMu.Lock()
	for _, v := range voices {
		pWaveOutReset.Call(v.handle)
		pWaveOutUnprepareHeader.Call(v.handle, uintptr(unsafe.Pointer(v.header)), unsafe.Sizeof(waveHeader{}))
		pWaveOutClose.Call(v.handle)
	}
	voices = nil
	voiceMu.Unlock()
}

func getCachedWav(path string, volume float64) (cachedWav, bool) {
	key := fmt.Sprintf("%s@%.2f", path, volume)
	pcmCacheMu.Lock()
	if c, ok := pcmCache[key]; ok {
		pcmCacheMu.Unlock()
		return c, true
	}
	pcmCacheMu.Unlock()

	data := mustAsset(path)
	if len(data) < 12 || string(data[:4]) != "RIFF" {
		return cachedWav{}, false
	}
	var wf waveFormat
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		start := off + 8
		if size < 0 || start+size > len(data) {
			return cachedWav{}, false
		}
		chunk := data[start : start+size]
		switch string(data[off : off+4]) {
		case "fmt ":
			if len(chunk) < 16 {
				return cachedWav{}, false
			}
			wf = waveFormat{Tag: binary.LittleEndian.Uint16(chunk), Channels: binary.LittleEndian.Uint16(chunk[2:]), Rate: binary.LittleEndian.Uint32(chunk[4:]), BytesPerSec: binary.LittleEndian.Uint32(chunk[8:]), BlockAlign: binary.LittleEndian.Uint16(chunk[12:]), Bits: binary.LittleEndian.Uint16(chunk[14:])}
		case "data":
			pcm = chunk
		}
		off = start + size + (size & 1)
	}
	if wf.Tag != 1 || wf.Bits != 16 || len(pcm) == 0 {
		return cachedWav{}, false
	}
	scaled := make([]byte, len(pcm))
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(binary.LittleEndian.Uint16(pcm[i:]))
		binary.LittleEndian.PutUint16(scaled[i:], uint16(int16(float64(v)*volume)))
	}
	c := cachedWav{format: wf, pcm: scaled}
	pcmCacheMu.Lock()
	pcmCache[key] = c
	pcmCacheMu.Unlock()
	return c, true
}

func playSoundAsset(path string) { playSoundVolume(path, .7) }
func playSoundVolume(path string, volume float64) {
	if volume <= 0 {
		return
	}
	voiceMu.Lock()
	if activeAudio >= 8 {
		voiceMu.Unlock()
		return
	}
	activeAudio++
	voiceMu.Unlock()

	go func() {
		defer func() {
			voiceMu.Lock()
			activeAudio--
			voiceMu.Unlock()
		}()
		cw, ok := getCachedWav(path, volume)
		if !ok {
			return
		}
		var handle uintptr
		wf := cw.format
		if r, _, _ := pWaveOutOpen.Call(uintptr(unsafe.Pointer(&handle)), 0xffffffff, uintptr(unsafe.Pointer(&wf)), 0, 0, 0); r != 0 {
			return
		}
		hdr := &waveHeader{Data: &cw.pcm[0], Length: uint32(len(cw.pcm))}
		if r, _, _ := pWaveOutPrepareHeader.Call(handle, uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(waveHeader{})); r != 0 {
			pWaveOutClose.Call(handle)
			return
		}
		if r, _, _ := pWaveOutWrite.Call(handle, uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(waveHeader{})); r != 0 {
			pWaveOutUnprepareHeader.Call(handle, uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(waveHeader{}))
			pWaveOutClose.Call(handle)
			return
		}
		for hdr.Flags&1 == 0 {
			time.Sleep(25 * time.Millisecond)
		}
		pWaveOutUnprepareHeader.Call(handle, uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(waveHeader{}))
		pWaveOutClose.Call(handle)
	}()
}
