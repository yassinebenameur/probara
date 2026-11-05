import assert from 'node:assert/strict';
import test from 'node:test';
import { encodeQr, qrPath } from './qr';

// Format bits are a BCH(15,5) code: the 15 bits read back from beside the
// top-left finder must decode (ECC level M = 0b00) with a valid remainder.
function readFormat(m: boolean[][]): number {
  let bits = 0;
  const at = (x: number, y: number) => (m[y][x] ? 1 : 0);
  const order: Array<[number, number]> = [
    [8, 0], [8, 1], [8, 2], [8, 3], [8, 4], [8, 5], [8, 7], [8, 8], [7, 8],
    [5, 8], [4, 8], [3, 8], [2, 8], [1, 8], [0, 8],
  ];
  order.forEach(([x, y], i) => {
    bits |= at(x, y) << i;
  });
  return bits ^ 0x5412;
}

test('sizes the symbol by content and draws the fixed patterns', () => {
  for (const [len, version] of [[1, 1], [14, 1], [15, 2], [60, 4], [150, 8], [213, 10]] as const) {
    const m = encodeQr('a'.repeat(len));
    assert.equal(m.length, version * 4 + 17, `length ${len}`);
    const s = m.length;
    // Finder corners: dark outer ring, light separator ring, dark core.
    for (const [cx, cy] of [[3, 3], [s - 4, 3], [3, s - 4]]) {
      assert.equal(m[cy - 3][cx - 3], true);
      assert.equal(m[cy - 2][cx - 2], false);
      assert.equal(m[cy][cx], true);
    }
    // Timing pattern alternates.
    for (let i = 8; i < s - 8; i++) assert.equal(m[6][i], i % 2 === 0);
    const format = readFormat(m);
    assert.equal(format >>> 13, 0, 'level M');
    let rem = format >>> 10;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    assert.equal(((format >>> 10) << 10) | rem, format, 'BCH remainder');
  }
});

test('refuses text past version 10', () => {
  assert.throws(() => encodeQr('a'.repeat(214)));
});

test('renders dark modules inside the quiet zone', () => {
  const path = qrPath([[true, false], [false, true]], 4);
  assert.equal(path, 'M4,4h1v1h-1zM5,5h1v1h-1z');
});
