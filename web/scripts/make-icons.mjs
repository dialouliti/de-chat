// Generates the two PNG icons referenced by the PWA manifest. Produces
// minimal but valid PNGs (a solid brand-colored square) using only Node
// built-ins, so there's no native dep to install. Swap these for proper
// artwork before shipping.
//
// Run via: pnpm gen:icons   (or it's auto-run on first install)

import { writeFileSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import zlib from 'node:zlib'

const __dirname = dirname(fileURLToPath(import.meta.url))
const outDir = join(__dirname, '..', 'public', 'icons')
mkdirSync(outDir, { recursive: true })

// Brand blue, opaque.
const R = 0x3b
const G = 0x82
const B = 0xf6

function makePng(size) {
  // Each row: a leading filter byte (0) then size pixels * 4 bytes (RGBA).
  const pixelRow = Buffer.alloc(size * 4)
  for (let i = 0; i < size; i++) {
    pixelRow[i * 4 + 0] = R
    pixelRow[i * 4 + 1] = G
    pixelRow[i * 4 + 2] = B
    pixelRow[i * 4 + 3] = 0xff
  }
  const row = Buffer.concat([Buffer.from([0]), pixelRow])
  const raw = Buffer.concat(Array.from({ length: size }, () => row))
  const idat = zlib.deflateSync(raw)

  // PNG signature + IHDR + IDAT + IEND, with CRC32 over each chunk.
  const signature = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])

  function chunk(type, data) {
    const len = Buffer.alloc(4)
    len.writeUInt32BE(data.length, 0)
    const typeBuf = Buffer.from(type, 'ascii')
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])), 0)
    return Buffer.concat([len, typeBuf, data, crc])
  }

  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(size, 0) // width
  ihdr.writeUInt32BE(size, 4) // height
  ihdr.writeUInt8(8, 8) // bit depth
  ihdr.writeUInt8(6, 9) // color type: RGBA
  ihdr.writeUInt8(0, 10) // compression
  ihdr.writeUInt8(0, 11) // filter
  ihdr.writeUInt8(0, 12) // interlace

  return Buffer.concat([
    signature,
    chunk('IHDR', ihdr),
    chunk('IDAT', idat),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

// Standard CRC32 (PNG uses the reflected polynomial).
const crcTable = (() => {
  const table = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) {
      c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    }
    table[n] = c >>> 0
  }
  return table
})()

function crc32(buf) {
  let c = 0xffffffff
  for (let i = 0; i < buf.length; i++) {
    c = crcTable[(c ^ buf[i]) & 0xff] ^ (c >>> 8)
  }
  return (c ^ 0xffffffff) >>> 0
}

for (const size of [192, 512]) {
  const png = makePng(size)
  const out = join(outDir, `icon-${size}.png`)
  writeFileSync(out, png)
  console.log(`wrote ${out} (${png.length} bytes)`)
}
