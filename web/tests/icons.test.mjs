import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8')
const publicDir = new URL('../public/', import.meta.url)
const manifest = JSON.parse(await readFile(new URL('site.webmanifest', publicDir), 'utf8'))

test('页面引用本地 favicon、Apple 图标和清单', () => {
  for (const reference of [
    /rel="icon" href="\/favicon\.ico"/,
    /rel="icon" href="\/favicon\.svg" type="image\/svg\+xml"/,
    /rel="apple-touch-icon" href="\/apple-touch-icon\.png"/,
    /rel="manifest" href="\/site\.webmanifest"/,
  ]) assert.match(html, reference)
  assert.match(html, /name="theme-color" content="#174a83"/)
  assert.equal(manifest.theme_color, '#174a83')
})

test('位图尺寸与清单声明一致，ICO 包含常用尺寸', async () => {
  for (const [path, size] of [['apple-touch-icon.png', 180], ['icon-192.png', 192], ['icon-512.png', 512]]) {
    const png = await readFile(new URL(path, publicDir))
    assert.equal(png.subarray(0, 8).toString('hex'), '89504e470d0a1a0a')
    assert.equal(png.readUInt32BE(16), size)
    assert.equal(png.readUInt32BE(20), size)
  }
  assert.deepEqual(manifest.icons.map(({ src, sizes, type }) => [src, sizes, type]), [
    ['/icon-192.png', '192x192', 'image/png'],
    ['/icon-512.png', '512x512', 'image/png'],
  ])
  const ico = await readFile(new URL('favicon.ico', publicDir))
  assert.equal(ico.readUInt16LE(0), 0)
  assert.equal(ico.readUInt16LE(2), 1)
  assert.deepEqual(Array.from({ length: ico.readUInt16LE(4) }, (_, i) => ico[6 + 16 * i]), [16, 32, 48, 64])
  assert.match(await readFile(new URL('favicon.svg', publicDir), 'utf8'), /<svg[^>]*viewBox="0 0 512 512"/)
})
