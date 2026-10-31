import {
  BALL_D,
  BALL_R,
  BREAK_SPOT,
  CORNER_FACING_DEG,
  CORNER_MOUTH,
  CUSHION_DEPTH,
  FOOT_SPOT,
  HEAD_SPOT,
  HEAD_STRING_X,
  JAW_RADIUS,
  RACK_GAP,
  SIDE_FACING_DEG,
  SIDE_MOUTH,
  TABLE_L,
  TABLE_W,
} from "./constants";
import type { Rng } from "./rng";
import type { Ball, BallInHand, Vec } from "./types";

/**
 * A straight piece of cushion. `n` is the unit normal on the side a ball can
 * reach it from; balls collide with it as a capsule of radius BALL_R.
 */
export interface Segment {
  ax: number;
  ay: number;
  bx: number;
  by: number;
  /** Unit direction a → b. */
  dx: number;
  dy: number;
  len: number;
  nx: number;
  ny: number;
}

/** A rounded jaw or a facing's end: a ball touches it at distance R + r. */
export interface CircleCollider {
  x: number;
  y: number;
  r: number;
}

export interface Pocket {
  kind: "corner" | "side";
  /** Capture centre: a ball whose centre comes within `r` of it drops. */
  x: number;
  y: number;
  r: number;
  /** Midpoint between the two jaw points, on the cushion nose line. */
  mouthX: number;
  mouthY: number;
  /** Unit vector from the mouth into the pocket (out of the table). */
  outX: number;
  outY: number;
  /** Radius of the drawn hole. */
  holeR: number;
}

export interface TableGeometry {
  segments: Segment[];
  circles: CircleCollider[];
  pockets: Pocket[];
  /** Cushion outlines (rail side included) for drawing, one polygon each. */
  cushionPolys: Vec[][];
}

/*
 * Pocket indices run clockwise from the head-end top corner:
 *   0 ─────── 1 ─────── 2
 *   │                   │
 *   5 ─────── 4 ─────── 3
 */
const CORNER_CAPTURE_DEPTH = 10; // capture centre, outward along the bisector
const CORNER_CAPTURE_R = 37;
const SIDE_CAPTURE_DEPTH = 26; // capture centre, outward from the nose line
const SIDE_CAPTURE_R = 38;

function makeSegment(a: Vec, b: Vec): Segment {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.hypot(dx, dy);
  const ux = dx / len;
  const uy = dy / len;
  // Cushions are laid out clockwise (y points down), so the playable side of
  // every segment is on its right-hand normal (-dy, dx).
  return { ax: a.x, ay: a.y, bx: b.x, by: b.y, dx: ux, dy: uy, len, nx: -uy, ny: ux };
}

/**
 * Round the convex corner where segment `p → q` meets `q → r` with a fillet of
 * `radius`, tucked inside the cushion. Returns the circle and the two tangent
 * points, which become the new ends of the trimmed segments.
 */
function fillet(p: Vec, q: Vec, r: Vec, radius: number) {
  const s1 = makeSegment(p, q);
  const s2 = makeSegment(q, r);
  // Solve (c - q)·n1 = -radius and (c - q)·n2 = -radius for the centre c.
  const det = s1.nx * s2.ny - s1.ny * s2.nx;
  const cx = q.x + (radius * (s1.ny - s2.ny)) / det;
  const cy = q.y + (radius * (s2.nx - s1.nx)) / det;
  return {
    circle: { x: cx, y: cy, r: radius },
    t1: { x: cx + radius * s1.nx, y: cy + radius * s1.ny },
    t2: { x: cx + radius * s2.nx, y: cy + radius * s2.ny },
  };
}

/** Points along the short arc of `c` from `from` to `to`, both included. */
function arcPoints(c: CircleCollider, from: Vec, to: Vec, steps = 6): Vec[] {
  const a0 = Math.atan2(from.y - c.y, from.x - c.x);
  let sweep = Math.atan2(to.y - c.y, to.x - c.x) - a0;
  while (sweep > Math.PI) sweep -= Math.PI * 2;
  while (sweep < -Math.PI) sweep += Math.PI * 2;
  const pts: Vec[] = [];
  for (let i = 0; i <= steps; i++) {
    const a = a0 + (sweep * i) / steps;
    pts.push({ x: c.x + Math.cos(a) * c.r, y: c.y + Math.sin(a) * c.r });
  }
  return pts;
}

interface CushionSpec {
  /** Jaw points on the nose line, in clockwise order. */
  j1: Vec;
  j2: Vec;
  /** Facing angle at each end (degrees between the nose and the facing). */
  a1: number;
  a2: number;
}

function buildTable(): TableGeometry {
  const L = TABLE_L;
  const W = TABLE_W;
  const cm = CORNER_MOUTH / Math.SQRT2; // jaw distance from the corner along a rail
  const sm = SIDE_MOUTH / 2;
  const C = CORNER_FACING_DEG;
  const S = SIDE_FACING_DEG;

  const specs: CushionSpec[] = [
    { j1: { x: cm, y: 0 }, j2: { x: L / 2 - sm, y: 0 }, a1: C, a2: S },
    { j1: { x: L / 2 + sm, y: 0 }, j2: { x: L - cm, y: 0 }, a1: S, a2: C },
    { j1: { x: L, y: cm }, j2: { x: L, y: W - cm }, a1: C, a2: C },
    { j1: { x: L - cm, y: W }, j2: { x: L / 2 + sm, y: W }, a1: C, a2: S },
    { j1: { x: L / 2 - sm, y: W }, j2: { x: cm, y: W }, a1: S, a2: C },
    { j1: { x: 0, y: W - cm }, j2: { x: 0, y: cm }, a1: C, a2: C },
  ];

  const segments: Segment[] = [];
  const circles: CircleCollider[] = [];
  const cushionPolys: Vec[][] = [];

  for (const { j1, j2, a1, a2 } of specs) {
    const main = makeSegment(j1, j2);
    // d runs along the cushion, o points out of the table into the rail.
    const d = { x: main.dx, y: main.dy };
    const o = { x: -main.nx, y: -main.ny };
    const facingEnd = (j: Vec, angleDeg: number, back: boolean): Vec => {
      const a = (angleDeg * Math.PI) / 180;
      const along = back ? Math.cos(a) : -Math.cos(a);
      const k = CUSHION_DEPTH / Math.sin(a);
      return {
        x: j.x + k * (along * d.x + Math.sin(a) * o.x),
        y: j.y + k * (along * d.y + Math.sin(a) * o.y),
      };
    };
    const f1 = facingEnd(j1, a1, true);
    const f2 = facingEnd(j2, a2, false);

    const jaw1 = fillet(f1, j1, j2, JAW_RADIUS);
    const jaw2 = fillet(j1, j2, f2, JAW_RADIUS);

    segments.push(makeSegment(f1, jaw1.t1));
    segments.push(makeSegment(jaw1.t2, jaw2.t1));
    segments.push(makeSegment(jaw2.t2, f2));
    circles.push(jaw1.circle, jaw2.circle);
    // The far ends of the facings sit deep in the pocket throat; give them a
    // point collider so a ball can never slip past one edge-on.
    circles.push({ x: f1.x, y: f1.y, r: 0 }, { x: f2.x, y: f2.y, r: 0 });

    // Drawing outline: facing, rounded jaw, nose, rounded jaw, facing. Both
    // facings end on the rail face, so closing the polygon runs along it.
    cushionPolys.push([
      f1,
      ...arcPoints(jaw1.circle, jaw1.t1, jaw1.t2),
      ...arcPoints(jaw2.circle, jaw2.t1, jaw2.t2),
      f2,
    ]);
  }

  const corner = (cx: number, cy: number, ox: number, oy: number): Pocket => {
    const inv = 1 / Math.SQRT2;
    return {
      kind: "corner",
      x: cx + ox * inv * CORNER_CAPTURE_DEPTH,
      y: cy + oy * inv * CORNER_CAPTURE_DEPTH,
      r: CORNER_CAPTURE_R,
      mouthX: cx - (ox * cm) / 2,
      mouthY: cy - (oy * cm) / 2,
      outX: ox * inv,
      outY: oy * inv,
      holeR: BALL_R * 1.62,
    };
  };
  const side = (cx: number, cy: number, oy: number): Pocket => ({
    kind: "side",
    x: cx,
    y: cy + oy * SIDE_CAPTURE_DEPTH,
    r: SIDE_CAPTURE_R,
    mouthX: cx,
    mouthY: cy,
    outX: 0,
    outY: oy,
    holeR: BALL_R * 1.55,
  });

  const pockets: Pocket[] = [
    corner(0, 0, -1, -1),
    side(L / 2, 0, -1),
    corner(L, 0, 1, -1),
    corner(L, W, 1, 1),
    side(L / 2, W, 1),
    corner(0, W, -1, 1),
  ];

  return { segments, circles, pockets, cushionPolys };
}

/** The one table every shot is played on. */
export const TABLE: TableGeometry = buildTable();

/* ---------------------------------------------------------------- balls */

export const isSolid = (id: number) => id >= 1 && id <= 7;
export const isStripe = (id: number) => id >= 9 && id <= 15;

export function makeBall(id: number, x: number, y: number): Ball {
  return { id, x, y, vx: 0, vy: 0, ux: 0, uy: 0, pocket: -1 };
}

export function cloneBalls(balls: readonly Ball[]): Ball[] {
  return balls.map((b) => ({ ...b }));
}

/** Centres of the fifteen rack positions, apex first, row by row. */
export function rackSlots(): Vec[] {
  const pitch = BALL_D + RACK_GAP;
  const dx = pitch * Math.cos(Math.PI / 6);
  const slots: Vec[] = [];
  for (let row = 0; row < 5; row++) {
    for (let k = 0; k <= row; k++) {
      slots.push({ x: FOOT_SPOT.x + row * dx, y: FOOT_SPOT.y + (k - row / 2) * pitch });
    }
  }
  return slots;
}

/**
 * A standard 8-ball rack: the 8 in the middle of the third row, a solid in one
 * back corner and a stripe in the other, every other ball shuffled. The array
 * is indexed by ball number (index 0 is the cue ball, placed for the break).
 */
export function rackBalls(rng: Rng): Ball[] {
  const slots = rackSlots();
  const EIGHT_SLOT = 4; // third row, middle
  const BACK_LEFT = 10;
  const BACK_RIGHT = 14;

  const solids = [1, 2, 3, 4, 5, 6, 7];
  const stripes = [9, 10, 11, 12, 13, 14, 15];
  const cornerSolid = solids.splice(rng.int(0, solids.length - 1), 1)[0];
  const cornerStripe = stripes.splice(rng.int(0, stripes.length - 1), 1)[0];
  const rest = rng.shuffle([...solids, ...stripes]);

  const at: number[] = new Array(15).fill(0);
  at[EIGHT_SLOT] = 8;
  const solidLeft = rng.next() < 0.5;
  at[BACK_LEFT] = solidLeft ? cornerSolid : cornerStripe;
  at[BACK_RIGHT] = solidLeft ? cornerStripe : cornerSolid;
  let next = 0;
  for (let i = 0; i < 15; i++) {
    if (at[i] === 0) at[i] = rest[next++];
  }

  const balls: Ball[] = new Array(16);
  balls[0] = makeBall(0, BREAK_SPOT.x, BREAK_SPOT.y);
  for (let i = 0; i < 15; i++) {
    const id = at[i];
    balls[id] = makeBall(id, slots[i].x, slots[i].y);
  }
  return balls;
}

/* ------------------------------------------------------------ placement */

/** Bounds a cue ball in hand may be dropped within. */
export function placementBounds(zone: BallInHand) {
  return {
    minX: BALL_R,
    maxX: zone === "kitchen" ? HEAD_STRING_X : TABLE_L - BALL_R,
    minY: BALL_R,
    maxY: TABLE_W - BALL_R,
  };
}

/** Clamp a dragged cue-ball position into its legal zone. */
export function clampToZone(x: number, y: number, zone: BallInHand): Vec {
  const b = placementBounds(zone);
  return {
    x: Math.min(b.maxX, Math.max(b.minX, x)),
    y: Math.min(b.maxY, Math.max(b.minY, y)),
  };
}

/** True when ball `id` could sit at (x, y) without touching another ball. */
export function isFree(balls: readonly Ball[], x: number, y: number, id: number): boolean {
  const min2 = (BALL_D + 0.01) * (BALL_D + 0.01);
  for (const b of balls) {
    if (b.id === id || b.pocket >= 0) continue;
    const dx = b.x - x;
    const dy = b.y - y;
    if (dx * dx + dy * dy < min2) return false;
  }
  return true;
}

/** Can the cue ball be put down at (x, y) for this kind of ball in hand? */
export function isValidPlacement(balls: readonly Ball[], x: number, y: number, zone: BallInHand) {
  if (!Number.isFinite(x) || !Number.isFinite(y)) return false;
  const b = placementBounds(zone);
  const eps = 1e-6;
  if (x < b.minX - eps || x > b.maxX + eps || y < b.minY - eps || y > b.maxY + eps) return false;
  return isFree(balls, x, y, 0);
}

/**
 * The legal spot nearest to (x, y), searched outward on a fine spiral. Used
 * when a dragged cue ball would overlap another ball, and to return the cue
 * ball to the table after a scratch.
 */
export function nearestFreeSpot(balls: readonly Ball[], x: number, y: number, zone: BallInHand): Vec {
  const start = clampToZone(x, y, zone);
  if (isValidPlacement(balls, start.x, start.y, zone)) return start;
  for (let ring = 1; ring < 160; ring++) {
    const r = ring * 4;
    const steps = Math.max(12, Math.round((2 * Math.PI * r) / 6));
    for (let s = 0; s < steps; s++) {
      const a = (s / steps) * Math.PI * 2;
      const p = clampToZone(start.x + Math.cos(a) * r, start.y + Math.sin(a) * r, zone);
      if (isValidPlacement(balls, p.x, p.y, zone)) return p;
    }
  }
  return clampToZone(HEAD_SPOT.x, HEAD_SPOT.y, zone);
}

/**
 * Re-spot ball `id` on the foot spot or, if that is taken, as close behind it
 * on the long string as it fits (then in front of it), as the rules require.
 */
export function spotBall(balls: Ball[], id: number): void {
  const ball = balls[id];
  const y = FOOT_SPOT.y;
  const tryAt = (x: number) => {
    if (x < BALL_R || x > TABLE_L - BALL_R) return false;
    if (!isFree(balls, x, y, id)) return false;
    ball.x = x;
    ball.y = y;
    return true;
  };
  ball.vx = ball.vy = ball.ux = ball.uy = 0;
  ball.pocket = -1;
  for (let x = FOOT_SPOT.x; x <= TABLE_L - BALL_R; x += 1) if (tryAt(x)) return;
  for (let x = FOOT_SPOT.x; x >= BALL_R; x -= 1) if (tryAt(x)) return;
  const p = nearestFreeSpot(balls, FOOT_SPOT.x, FOOT_SPOT.y, "anywhere");
  ball.x = p.x;
  ball.y = p.y;
}
