package main

import (
	"encoding/binary"
	"math"
)

// trayIcon 生成 32x32 的红色圆环 ICO，避免额外维护二进制图标资源。
func trayIcon() []byte {
	const (
		size    = 32
		offset  = 22
		infoLen = 40
	)
	pixels := make([]byte, 0, size*size*4)
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			distance := math.Hypot(float64(x)-15.5, float64(y)-15.5)
			switch {
			case distance > 15.5:
				pixels = append(pixels, 0, 0, 0, 0)
			case distance > 11.5:
				pixels = append(pixels, 255, 255, 255, 255)
			default:
				pixels = append(pixels, 0x4d, 0x48, 0xe5, 255) // BGRA
			}
		}
	}
	mask := make([]byte, size*4)
	resourceLen := infoLen + len(pixels) + len(mask)

	le := binary.LittleEndian
	icon := make([]byte, 0, offset+resourceLen)
	icon = append(icon, 0, 0, 1, 0, 1, 0) // ICONDIR：保留、类型=图标、数量=1
	icon = append(icon, size, size, 0, 0) // 宽、高、调色板数、保留
	icon = le.AppendUint16(icon, 1)       // 平面数
	icon = le.AppendUint16(icon, 32)      // 位深
	icon = le.AppendUint32(icon, uint32(resourceLen))
	icon = le.AppendUint32(icon, offset)

	icon = le.AppendUint32(icon, infoLen) // BITMAPINFOHEADER
	icon = le.AppendUint32(icon, size)
	icon = le.AppendUint32(icon, size*2) // XOR + AND 掩码
	icon = le.AppendUint16(icon, 1)
	icon = le.AppendUint16(icon, 32)
	icon = le.AppendUint32(icon, 0)
	icon = le.AppendUint32(icon, uint32(len(pixels)))
	icon = le.AppendUint32(icon, 0)
	icon = le.AppendUint32(icon, 0)
	icon = le.AppendUint32(icon, 0)
	icon = le.AppendUint32(icon, 0)

	icon = append(icon, pixels...)
	icon = append(icon, mask...)
	return icon
}
