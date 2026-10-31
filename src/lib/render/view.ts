import { TABLE_L, TABLE_W } from "../game/constants";
import type { Vec } from "../game/types";

/** Rail and frame width beyond the cushion nose, mm. */
export const FRAME = 104;
const OUTER_L = TABLE_L + FRAME * 2;
const OUTER_W = TABLE_W + FRAME * 2;

/**
 * How the table sits on the canvas. In a tall box (a phone held upright) the
 * table is turned a quarter so its long side runs up the screen, with the
 * head end — where the break is played from — at the bottom.
 */
export interface View {
  /** Canvas size in CSS pixels, and the device pixel ratio. */
  cssW: number;
  cssH: number;
  dpr: number;
  rotated: boolean;
  /** CSS pixels per millimetre. */
  scale: number;
  /** The table→screen affine map (CSS px): sx = a·x + c·y + e, sy = b·x + d·y + f. */
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export function fitView(cssW: number, cssH: number, dpr: number): View {
  const rotated = cssH > cssW * 1.05;
  const outerW = rotated ? OUTER_W : OUTER_L;
  const outerH = rotated ? OUTER_L : OUTER_W;
  const margin = 6;
  const scale = Math.max(0.01, Math.min((cssW - margin * 2) / outerW, (cssH - margin * 2) / outerH));
  const left = (cssW - outerW * scale) / 2 + FRAME * scale;
  const top = (cssH - outerH * scale) / 2 + FRAME * scale;
  if (!rotated) {
    return { cssW, cssH, dpr, rotated, scale, a: scale, b: 0, c: 0, d: scale, e: left, f: top };
  }
  // x (head → foot) runs up the screen; y (top rail → bottom rail) runs right.
  return { cssW, cssH, dpr, rotated, scale, a: 0, b: -scale, c: scale, d: 0, e: left, f: top + TABLE_L * scale };
}

export function toScreen(v: View, x: number, y: number): Vec {
  return { x: v.a * x + v.c * y + v.e, y: v.b * x + v.d * y + v.f };
}

export function toTable(v: View, sx: number, sy: number): Vec {
  // Invert the 2×2 part; it is a scaled rotation, so this is exact.
  const det = v.a * v.d - v.b * v.c;
  const px = sx - v.e;
  const py = sy - v.f;
  return { x: (v.d * px - v.c * py) / det, y: (-v.b * px + v.a * py) / det };
}
