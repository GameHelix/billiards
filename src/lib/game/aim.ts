import { BALL_D, BALL_R } from "./constants";
import { toiCircle, toiSegment } from "./physics";
import { TABLE } from "./table";
import type { Ball, Vec } from "./types";

export interface SweepHit {
  kind: "ball" | "cushion" | "pocket" | "none";
  /** Distance the swept centre travels before the contact. */
  dist: number;
  /** Ball touched or pocket entered; -1 otherwise. */
  id: number;
  /** Unit contact normal, pointing from the obstacle back to the swept ball. */
  nx: number;
  ny: number;
}

const NO_BALLS: readonly Ball[] = [];

/**
 * Sweep a ball from (x, y) along the unit direction (dx, dy) and report the
 * first thing it would reach: another ball (two may be skipped by number), a
 * cushion, or — when `pockets` is set — a pocket's capture circle. It uses the
 * very same contact tests as the simulation, so a straight-line prediction
 * here is exactly what the physics will do until friction or spin bends it.
 */
export function sweep(
  balls: readonly Ball[],
  x: number,
  y: number,
  dx: number,
  dy: number,
  skipA: number,
  skipB: number,
  pockets: boolean,
  maxDist = 6000
): SweepHit {
  let best = maxDist;
  let kind: SweepHit["kind"] = "none";
  let id = -1;
  let cx = 0;
  let cy = 0;
  let nx = 0;
  let ny = 0;

  for (const b of balls) {
    if (b.pocket >= 0 || b.id === skipA || b.id === skipB) continue;
    const t = toiCircle(x, y, dx, dy, b.x, b.y, BALL_D, best);
    if (t >= 0 && t < best) {
      best = t;
      kind = "ball";
      id = b.id;
      cx = b.x;
      cy = b.y;
    }
  }
  for (const s of TABLE.segments) {
    const t = toiSegment(x, y, dx, dy, s, best);
    if (t >= 0 && t < best) {
      best = t;
      kind = "cushion";
      id = -1;
      nx = s.nx;
      ny = s.ny;
    }
  }
  for (const c of TABLE.circles) {
    const t = toiCircle(x, y, dx, dy, c.x, c.y, BALL_R + c.r, best);
    if (t >= 0 && t < best) {
      best = t;
      kind = "cushion";
      id = -2; // marks a circle: the normal is radial
      cx = c.x;
      cy = c.y;
    }
  }
  if (pockets) {
    TABLE.pockets.forEach((p, i) => {
      const t = toiCircle(x, y, dx, dy, p.x, p.y, p.r, best);
      if (t >= 0 && t < best) {
        best = t;
        kind = "pocket";
        id = i;
      }
    });
  }

  if (kind === "ball" || id === -2) {
    const hx = x + dx * best - cx;
    const hy = y + dy * best - cy;
    const d = Math.hypot(hx, hy) || 1;
    nx = hx / d;
    ny = hy / d;
    if (id === -2) id = -1;
  }
  return { kind, dist: best, id, nx, ny };
}

/** Sweep against the cushions and pockets only, as if the table were empty. */
export function sweepEmpty(x: number, y: number, dx: number, dy: number, maxDist = 6000): SweepHit {
  return sweep(NO_BALLS, x, y, dx, dy, -1, -1, true, maxDist);
}

export interface AimGuide {
  /** Cue-ball centre where it first touches something (the ghost ball). */
  end: Vec;
  kind: SweepHit["kind"];
  /** Object ball that would be struck first, or -1. */
  targetId: number;
  /** Where the struck object ball heads (along the line of centres). */
  objectDir: Vec | null;
  /** Where the cue ball glances off to: the tangent line, or the mirror off a cushion. */
  cueDir: Vec | null;
  /** Cut angle in radians for a ball contact (0 = full ball). */
  cut: number;
}

/** The aiming guide for a cue ball at rest, sent along `angle`. */
export function aimGuide(balls: readonly Ball[], angle: number): AimGuide {
  const cue = balls[0];
  const dx = Math.cos(angle);
  const dy = Math.sin(angle);
  const hit = sweep(balls, cue.x, cue.y, dx, dy, 0, -1, true);
  const end = { x: cue.x + dx * hit.dist, y: cue.y + dy * hit.dist };

  if (hit.kind === "ball") {
    // The object ball leaves along the line of centres; a stunned cue ball
    // leaves along the tangent line, at right angles to it.
    const ox = -hit.nx;
    const oy = -hit.ny;
    const along = dx * ox + dy * oy;
    let tx = dx - along * ox;
    let ty = dy - along * oy;
    const tl = Math.hypot(tx, ty);
    let cueDir: Vec | null = null;
    if (tl > 1e-3) {
      tx /= tl;
      ty /= tl;
      cueDir = { x: tx, y: ty };
    }
    return {
      end,
      kind: "ball",
      targetId: hit.id,
      objectDir: { x: ox, y: oy },
      cueDir,
      cut: Math.acos(Math.max(-1, Math.min(1, along))),
    };
  }
  if (hit.kind === "cushion") {
    const dn = dx * hit.nx + dy * hit.ny;
    return {
      end,
      kind: "cushion",
      targetId: -1,
      objectDir: null,
      cueDir: { x: dx - 2 * dn * hit.nx, y: dy - 2 * dn * hit.ny },
      cut: 0,
    };
  }
  return { end, kind: hit.kind, targetId: -1, objectDir: null, cueDir: null, cut: 0 };
}
