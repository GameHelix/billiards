import { BALL_COLORS, BALL_R } from "../game/constants";
import { isStripe } from "../game/table";
import type { Ball } from "../game/types";

/*
 * Balls are drawn as little spheres that really roll. Each keeps an
 * orientation (a unit quaternion, body → table frame), turned every frame by
 * the distance it has travelled, so the numbers and stripes tumble the way
 * they would on cloth. The frame used here has x and y on the table and z
 * pointing away from the viewer, so the visible half of a ball is z < 0.
 */

export type Quat = [number, number, number, number]; // w, x, y, z
type V3 = [number, number, number];

const IDENTITY: Quat = [1, 0, 0, 0];

function qmul(a: Quat, b: Quat): Quat {
  return [
    a[0] * b[0] - a[1] * b[1] - a[2] * b[2] - a[3] * b[3],
    a[0] * b[1] + a[1] * b[0] + a[2] * b[3] - a[3] * b[2],
    a[0] * b[2] - a[1] * b[3] + a[2] * b[0] + a[3] * b[1],
    a[0] * b[3] + a[1] * b[2] - a[2] * b[1] + a[3] * b[0],
  ];
}

function qnormalize(q: Quat): Quat {
  const n = Math.hypot(q[0], q[1], q[2], q[3]) || 1;
  return [q[0] / n, q[1] / n, q[2] / n, q[3] / n];
}

function rotate(q: Quat, v: V3): V3 {
  const [w, x, y, z] = q;
  // v + 2w(q×v) + 2 q×(q×v), with q the vector part.
  const tx = 2 * (y * v[2] - z * v[1]);
  const ty = 2 * (z * v[0] - x * v[2]);
  const tz = 2 * (x * v[1] - y * v[0]);
  return [v[0] + w * tx + (y * tz - z * ty), v[1] + w * ty + (z * tx - x * tz), v[2] + w * tz + (x * ty - y * tx)];
}

/** Per-ball orientations, rolled along by the balls' motion. */
export class BallSpinner {
  private orient: Quat[] = [];
  private lastX: number[] = [];
  private lastY: number[] = [];

  orientation(id: number): Quat {
    return this.orient[id] ?? IDENTITY;
  }

  /** Roll every ball by how far it moved since the last call. */
  update(balls: readonly Ball[]): void {
    for (const b of balls) {
      const id = b.id;
      if (this.orient[id] === undefined) {
        // Start with the number facing up, turned a little so the rack
        // does not look stamped out.
        const a = (id * 0.61) % (Math.PI * 2);
        this.orient[id] = [Math.cos(a / 2), 0, 0, Math.sin(a / 2)];
        this.lastX[id] = b.x;
        this.lastY[id] = b.y;
        continue;
      }
      const dx = b.x - this.lastX[id];
      const dy = b.y - this.lastY[id];
      this.lastX[id] = b.x;
      this.lastY[id] = b.y;
      const dist = Math.hypot(dx, dy);
      if (dist < 1e-6 || dist > BALL_R * 40) continue;
      // Rolling on the cloth (behind the ball, at +z): ω ∝ (dy, −dx, 0).
      const angle = dist / BALL_R;
      const s = Math.sin(angle / 2) / dist;
      const dq: Quat = [Math.cos(angle / 2), dy * s, -dx * s, 0];
      this.orient[id] = qnormalize(qmul(dq, this.orient[id]));
    }
  }
}

/* ------------------------------------------------------------- sprites */

const spriteCache = new Map<string, HTMLCanvasElement>();

function sprite(key: string, size: number, paint: (ctx: CanvasRenderingContext2D, s: number) => void) {
  let c = spriteCache.get(key);
  if (!c) {
    c = document.createElement("canvas");
    c.width = c.height = Math.max(2, Math.ceil(size));
    const ctx = c.getContext("2d");
    if (ctx) paint(ctx, c.width);
    if (spriteCache.size > 160) spriteCache.clear();
    spriteCache.set(key, c);
  }
  return c;
}

/** Soft neon halo in the ball's colour, drawn under it. */
function glowSprite(color: string, px: number) {
  const size = Math.ceil(px * 4.4);
  return sprite(`glow:${color}:${size}`, size, (ctx, s) => {
    const g = ctx.createRadialGradient(s / 2, s / 2, px * 0.6, s / 2, s / 2, s / 2);
    g.addColorStop(0, hexA(color, 0.42));
    g.addColorStop(0.45, hexA(color, 0.14));
    g.addColorStop(1, hexA(color, 0));
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, s, s);
  });
}

/** Light from the upper left: a rim falloff and a specular glint. */
function shadeSprite(px: number) {
  const size = Math.ceil(px * 2);
  return sprite(`shade:${size}`, size, (ctx, s) => {
    const r = s / 2;
    const rim = ctx.createRadialGradient(r * 0.78, r * 0.72, r * 0.15, r, r, r);
    rim.addColorStop(0, "rgba(255,255,255,0.10)");
    rim.addColorStop(0.55, "rgba(0,0,0,0)");
    rim.addColorStop(0.86, "rgba(0,0,10,0.32)");
    rim.addColorStop(1, "rgba(0,0,10,0.62)");
    ctx.fillStyle = rim;
    ctx.beginPath();
    ctx.arc(r, r, r, 0, Math.PI * 2);
    ctx.fill();
    const glint = ctx.createRadialGradient(r * 0.62, r * 0.55, 0, r * 0.62, r * 0.55, r * 0.42);
    glint.addColorStop(0, "rgba(255,255,255,0.85)");
    glint.addColorStop(0.35, "rgba(255,255,255,0.28)");
    glint.addColorStop(1, "rgba(255,255,255,0)");
    ctx.fillStyle = glint;
    ctx.beginPath();
    ctx.arc(r, r, r, 0, Math.PI * 2);
    ctx.fill();
  });
}

function hexA(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${alpha})`;
}

/* ------------------------------------------------------------ drawing */

/** Body-frame features: the number sits on the z poles, the stripe circles y. */
const POLE: V3 = [0, 0, -1];
const STRIPE_AXIS: V3 = [0, 1, 0];
const BAND = 0.52; // the stripe covers |q·axis| < BAND
const DISC = 0.43; // sine of the number disc's angular radius
const DISC_COS = Math.sqrt(1 - DISC * DISC);

/** Map a table-frame vector to the screen frame (the view may be turned). */
function toScreen3(v: V3, rotated: boolean): V3 {
  return rotated ? [v[1], -v[0], v[2]] : v;
}

/**
 * Fill the visible part of the cap {q : q·a > h} of a unit sphere drawn at
 * (cx, cy) with radius r. `a` is in screen frame, z away from the viewer.
 */
function fillCap(ctx: CanvasRenderingContext2D, cx: number, cy: number, r: number, a: V3, h: number) {
  const ax = a[0];
  const ay = a[1];
  const az = -a[2]; // toward the viewer
  const rc = Math.sqrt(1 - h * h);
  const s = Math.hypot(ax, ay);
  ctx.beginPath();
  if (s < 1e-6) {
    if (az <= 0) return;
    ctx.arc(cx + h * ax * r, cy + h * ay * r, rc * r, 0, Math.PI * 2);
    ctx.fill();
    return;
  }
  // Circle of the cap: q(φ) = h·a + rc(e1 cos φ + e2 sin φ), e1 level with the screen.
  const e1x = ay / s;
  const e1y = -ax / s;
  const e2x = (az * ax) / s;
  const e2y = (az * ay) / s;
  const k = (h * az) / (rc * s); // visible where sin φ ≤ k
  if (k <= -1) return;
  const point = (phi: number) => {
    const c = Math.cos(phi);
    const sn = Math.sin(phi);
    return [cx + (h * ax + rc * (e1x * c + e2x * sn)) * r, cy + (h * ay + rc * (e1y * c + e2y * sn)) * r];
  };
  const N = 28;
  if (k >= 1) {
    for (let i = 0; i <= N; i++) {
      const [x, y] = point((i / N) * Math.PI * 2);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.closePath();
    ctx.fill();
    return;
  }
  const as = Math.asin(k);
  const p0 = Math.PI - as;
  const p1 = 2 * Math.PI + as;
  for (let i = 0; i <= N; i++) {
    const [x, y] = point(p0 + ((p1 - p0) * i) / N);
    if (i === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  }
  // Close along the silhouette, round the side the cap lies on.
  const [ex, ey] = point(p1);
  const [sx, sy] = point(p0);
  const t2 = Math.atan2(ey - cy, ex - cx);
  const t1 = Math.atan2(sy - cy, sx - cx);
  const ta = Math.atan2(ay, ax);
  const norm = (t: number) => ((t % (Math.PI * 2)) + Math.PI * 2) % (Math.PI * 2);
  const cwSweep = norm(t1 - t2);
  const viaCw = norm(ta - t2) <= cwSweep;
  ctx.arc(cx, cy, r, t2, t1, !viaCw);
  ctx.closePath();
  ctx.fill();
}

/** One number disc (white circle and digits) at body pole `pole`. */
function drawDisc(
  ctx: CanvasRenderingContext2D,
  q: Quat,
  rotated: boolean,
  cx: number,
  cy: number,
  r: number,
  pole: V3,
  right: V3,
  down: V3,
  label: string,
  dpr: number,
  dot: boolean
) {
  const p = toScreen3(rotate(q, pole), rotated);
  if (p[2] >= -0.02) return; // facing away
  const u = toScreen3(rotate(q, right), rotated);
  const w = toScreen3(rotate(q, down), rotated);
  // Decal space is drawn at 100× and shrunk by the transform, so the digits
  // are laid out at a sane font size in every browser.
  const k = r / 100;
  ctx.save();
  ctx.globalAlpha *= Math.min(1, -p[2] / 0.3);
  ctx.setTransform(
    dpr * u[0] * k,
    dpr * u[1] * k,
    dpr * w[0] * k,
    dpr * w[1] * k,
    dpr * (cx + p[0] * DISC_COS * r),
    dpr * (cy + p[1] * DISC_COS * r)
  );
  if (dot) {
    ctx.fillStyle = "#e11d48";
    ctx.beginPath();
    ctx.arc(0, 0, 13, 0, Math.PI * 2);
    ctx.fill();
  } else {
    ctx.fillStyle = "#f8fafc";
    ctx.beginPath();
    ctx.arc(0, 0, DISC * 100, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = "#0b0a14";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.font = `700 ${label.length > 1 ? 50 : 58}px ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif`;
    ctx.fillText(label, 0, 4);
  }
  ctx.restore();
}

/**
 * Draw ball `id` centred at (cx, cy) CSS px with radius r CSS px. The
 * context must carry the plain device-pixel transform.
 */
export function drawBall(
  ctx: CanvasRenderingContext2D,
  id: number,
  cx: number,
  cy: number,
  r: number,
  q: Quat,
  rotated: boolean,
  dpr: number,
  alpha = 1
) {
  const color = BALL_COLORS[id];
  ctx.save();
  ctx.globalAlpha = alpha;

  // Shadow and neon halo first, outside the ball.
  ctx.fillStyle = "rgba(0,0,0,0.38)";
  ctx.beginPath();
  ctx.ellipse(cx + r * 0.16, cy + r * 0.24, r * 1.02, r * 0.96, 0, 0, Math.PI * 2);
  ctx.fill();
  const glow = glowSprite(id === 8 ? "#a855f7" : color, r * dpr);
  ctx.drawImage(glow, cx - (glow.width / dpr) / 2, cy - (glow.height / dpr) / 2, glow.width / dpr, glow.height / dpr);

  ctx.beginPath();
  ctx.arc(cx, cy, r, 0, Math.PI * 2);
  ctx.clip();

  if (isStripe(id)) {
    ctx.fillStyle = color;
    ctx.fillRect(cx - r, cy - r, r * 2, r * 2);
    ctx.fillStyle = "#f4f4f5";
    const a = toScreen3(rotate(q, STRIPE_AXIS), rotated);
    fillCap(ctx, cx, cy, r, a, BAND);
    fillCap(ctx, cx, cy, r, [-a[0], -a[1], -a[2]], BAND);
  } else {
    ctx.fillStyle = color;
    ctx.fillRect(cx - r, cy - r, r * 2, r * 2);
  }

  const label = String(id);
  const cue = id === 0;
  drawDisc(ctx, q, rotated, cx, cy, r, POLE, [1, 0, 0], [0, 1, 0], label, dpr, cue);
  drawDisc(ctx, q, rotated, cx, cy, r, [0, 0, 1], [-1, 0, 0], [0, 1, 0], label, dpr, cue);

  const shade = shadeSprite(r * dpr);
  ctx.drawImage(shade, cx - r, cy - r, r * 2, r * 2);
  ctx.restore();
}
