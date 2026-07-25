/*
 * Generate placeholder favicon rasters (§8) with zero native dependencies:
 * a warm near-black rounded square with a purple mono-ish "K" drawn from a
 * bitmap glyph, encoded as PNG in pure JS (zlib is built into Node). Produces
 * favicon-16.png, favicon-32.png, apple-touch-icon.png (180) and a multi-res
 * favicon.ico (16/32/48). These are explicitly temporary until real brand art
 * exists; replacing them is a pure asset swap.
 */
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const publicDir = path.join(here, "..", "public");

const BG = [0x0e, 0x0e, 0x11]; // #0E0E11
const BORDER = [0x2a, 0x2a, 0x31]; // #2A2A31
const FG = [0x8b, 0x7f, 0xd6]; // #8B7FD6

// 8x8 bitmap of a "K" (1 = ink). Scaled up to the target size.
const K = [
  "1000001",
  "1000010",
  "1000100",
  "1111000",
  "1111000",
  "1000100",
  "1000010",
  "1000001",
].map((r) => r.split("").map(Number));

function renderRGBA(size) {
  const buf = Buffer.alloc(size * size * 4);
  const radius = Math.round(size * 0.125);
  const inCorner = (x, y) => {
    // rounded-corner mask
    const cx = x < radius ? radius : x >= size - radius ? size - radius - 1 : x;
    const cy = y < radius ? radius : y >= size - radius ? size - radius - 1 : y;
    return (x - cx) ** 2 + (y - cy) ** 2 <= radius * radius;
  };
  const glyphCols = K[0].length;
  const glyphRows = K.length;
  const scale = Math.floor((size * 0.62) / glyphRows);
  const gw = glyphCols * scale;
  const gh = glyphRows * scale;
  const ox = Math.floor((size - gw) / 2);
  const oy = Math.floor((size - gh) / 2);

  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const i = (y * size + x) * 4;
      const cornerX = x < radius || x >= size - radius;
      const cornerY = y < radius || y >= size - radius;
      const inside = !(cornerX && cornerY) || inCorner(x, y);
      if (!inside) {
        buf[i + 3] = 0; // transparent outside the rounded square
        continue;
      }
      let color = BG;
      // 1px border ring
      const edge = x <= 1 || y <= 1 || x >= size - 2 || y >= size - 2;
      if (edge) color = BORDER;
      // glyph
      const gx = Math.floor((x - ox) / scale);
      const gy = Math.floor((y - oy) / scale);
      if (gx >= 0 && gx < glyphCols && gy >= 0 && gy < glyphRows && K[gy][gx]) {
        color = FG;
      }
      buf[i] = color[0];
      buf[i + 1] = color[1];
      buf[i + 2] = color[2];
      buf[i + 3] = 255;
    }
  }
  return buf;
}

function crc32(buf) {
  let crc = ~0;
  for (let i = 0; i < buf.length; i++) {
    crc ^= buf[i];
    for (let k = 0; k < 8; k++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return (~crc) >>> 0;
}

function chunk(type, data) {
  const typeBuf = Buffer.from(type, "ascii");
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])), 0);
  return Buffer.concat([len, typeBuf, data, crc]);
}

function encodePNG(size, rgba) {
  const sig = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // color type RGBA
  // filter byte 0 per scanline
  const stride = size * 4;
  const raw = Buffer.alloc((stride + 1) * size);
  for (let y = 0; y < size; y++) {
    raw[y * (stride + 1)] = 0;
    rgba.copy(raw, y * (stride + 1) + 1, y * stride, y * stride + stride);
  }
  const idat = zlib.deflateSync(raw, { level: 9 });
  return Buffer.concat([
    sig,
    chunk("IHDR", ihdr),
    chunk("IDAT", idat),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

function encodeICO(pngs) {
  // ICONDIR
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2); // type: icon
  header.writeUInt16LE(pngs.length, 4);
  const entries = [];
  let offset = 6 + pngs.length * 16;
  const images = [];
  for (const { size, data } of pngs) {
    const e = Buffer.alloc(16);
    e[0] = size >= 256 ? 0 : size;
    e[1] = size >= 256 ? 0 : size;
    e[2] = 0; // colors
    e[3] = 0;
    e.writeUInt16LE(1, 4); // planes
    e.writeUInt16LE(32, 6); // bpp
    e.writeUInt32LE(data.length, 8);
    e.writeUInt32LE(offset, 12);
    offset += data.length;
    entries.push(e);
    images.push(data);
  }
  return Buffer.concat([header, ...entries, ...images]);
}

const outputs = [
  ["favicon-16.png", 16],
  ["favicon-32.png", 32],
  ["apple-touch-icon.png", 180],
];
for (const [name, size] of outputs) {
  const png = encodePNG(size, renderRGBA(size));
  fs.writeFileSync(path.join(publicDir, name), png);
  console.log(`gen-favicons: ${name} (${size}x${size}, ${png.length} B)`);
}

const icoSizes = [16, 32, 48];
const ico = encodeICO(icoSizes.map((size) => ({ size, data: encodePNG(size, renderRGBA(size)) })));
fs.writeFileSync(path.join(publicDir, "favicon.ico"), ico);
console.log(`gen-favicons: favicon.ico (${icoSizes.join("+")}, ${ico.length} B)`);
