import {
  BALL_D,
  BALL_R,
  BALL_RESTITUTION,
  CUSHION_GRIP,
  CUSHION_RESTITUTION,
  DRAG,
  MAX_SHOT_TIME,
  MAX_SPEED,
  MAX_SUBSTEPS,
  MAX_SUBSTEP_TRAVEL,
  MAX_TIP_OFFSET,
  ROLL_DECEL,
  SLIDE_DECEL,
  STOP_SPEED,
  TABLE_L,
  TABLE_W,
  TICK,
} from "./constants";
import { cloneBalls, TABLE, type Segment } from "./table";
import type { Ball, Impact, Shot, ShotEvents } from "./types";

/**
 * The table simulation.
 *
 * Time advances in fixed ticks; each tick is cut into sub-steps short enough
 * that friction stays smooth at break speed. Inside a sub-step every ball
 * moves in a straight line, so collisions are found *continuously*: the exact
 * time of the earliest contact is solved for, everything advances to it, that
 * one contact is resolved, and the search repeats for the rest of the
 * sub-step. A ball therefore can never tunnel through another ball or a
 * cushion however fast it goes, and a cut always happens at the true contact
 * point, which is what lets the ghost-ball aim of the guide and the CPU agree
 * with what the balls actually do.
 *
 * The same `step` drives the animated shot on screen and every shot the CPU
 * imagines, so a simulated outcome is exactly the outcome it will see.
 */
export interface Sim {
  /** Indexed by ball number; pocketed balls stay in place with `pocket ≥ 0`. */
  balls: Ball[];
  /** Seconds since the stroke. */
  time: number;
  events: ShotEvents;
  /** Contacts since the view last drained them (only if `recordImpacts`). */
  impacts: Impact[];
  recordImpacts: boolean;
  /** Every ball has stopped (or dropped). */
  settled: boolean;
}

const CONTACT2 = BALL_D * BALL_D;
/** Safety valve for a pathological pile-up inside one sub-step. */
const MAX_EVENTS = 256;
/** A capture can only happen this close to a cushion line. */
const EDGE_ZONE = BALL_R * 2.2;

export function emptyEvents(): ShotEvents {
  return { firstContact: null, railsAfterContact: 0, objectBallsToRail: [], pocketed: [], scratch: false };
}

export function createSim(balls: readonly Ball[], recordImpacts = true): Sim {
  return {
    balls: cloneBalls(balls),
    time: 0,
    events: emptyEvents(),
    impacts: [],
    recordImpacts,
    settled: true,
  };
}

/** Cue-ball speed for a power setting. */
export function powerToSpeed(power: number): number {
  return Math.min(1, Math.max(0, power)) * MAX_SPEED;
}

/**
 * Strike the cue ball. A tip contact `k` radii above centre gives the ball a
 * spin whose roll velocity is 5k/2 of its speed (natural roll at k = 2/5),
 * so follow keeps driving after the hit and draw pulls the cue ball back.
 */
export function strike(sim: Sim, shot: Shot): void {
  const cue = sim.balls[0];
  const speed = powerToSpeed(shot.power);
  const dx = Math.cos(shot.angle);
  const dy = Math.sin(shot.angle);
  const tip = Math.min(1, Math.max(-1, shot.spin)) * MAX_TIP_OFFSET;
  const roll = 2.5 * tip * speed;
  cue.vx = speed * dx;
  cue.vy = speed * dy;
  cue.ux = roll * dx;
  cue.uy = roll * dy;
  sim.events = emptyEvents();
  sim.time = 0;
  sim.settled = speed === 0;
}

/** Advance the simulation by one fixed tick. */
export function step(sim: Sim): void {
  if (sim.settled) return;
  const balls = sim.balls;
  let maxSpeed2 = 0;
  for (const b of balls) {
    if (b.pocket >= 0) continue;
    const s2 = b.vx * b.vx + b.vy * b.vy;
    if (s2 > maxSpeed2) maxSpeed2 = s2;
  }
  const n = Math.min(
    MAX_SUBSTEPS,
    Math.max(1, Math.ceil((Math.sqrt(maxSpeed2) * TICK) / MAX_SUBSTEP_TRAVEL))
  );
  const h = TICK / n;
  for (let i = 0; i < n; i++) {
    advance(sim, h);
    for (const b of balls) if (b.pocket < 0) applyFriction(b, h);
  }
  sim.time += TICK;

  let moving = false;
  for (const b of balls) {
    if (b.pocket < 0 && (b.vx !== 0 || b.vy !== 0 || b.ux !== 0 || b.uy !== 0)) {
      moving = true;
      break;
    }
  }
  if (!moving || sim.time >= MAX_SHOT_TIME) {
    for (const b of balls) b.vx = b.vy = b.ux = b.uy = 0;
    sim.settled = true;
  }
}

/** Run until everything has stopped. */
export function runToRest(sim: Sim): void {
  while (!sim.settled) step(sim);
}

export interface ShotResult {
  balls: Ball[];
  events: ShotEvents;
  /** Seconds until the last ball stopped. */
  time: number;
}

/** Play a whole shot from a position at rest without touching the input. */
export function simulateShot(balls: readonly Ball[], shot: Shot): ShotResult {
  const sim = createSim(balls, false);
  strike(sim, shot);
  runToRest(sim);
  return { balls: sim.balls, events: sim.events, time: sim.time };
}

/* ---------------------------------------------------------- the sweep */

function drift(balls: Ball[], t: number): void {
  for (const b of balls) {
    if (b.pocket >= 0) continue;
    b.x += b.vx * t;
    b.y += b.vy * t;
  }
}

/**
 * Earliest t in [0, tMax] at which a point moving from p with velocity v comes
 * within `rr` of the fixed centre c, or -1 if it does not (or is moving away).
 * Two balls are the same problem in relative terms, with rr = 2R.
 */
export function toiCircle(
  px: number,
  py: number,
  vx: number,
  vy: number,
  cx: number,
  cy: number,
  rr: number,
  tMax: number
): number {
  const dx = px - cx;
  const dy = py - cy;
  const bq = dx * vx + dy * vy;
  if (bq >= 0) return -1; // separating, or not moving relative to it
  const c = dx * dx + dy * dy - rr * rr;
  if (c <= 0) return 0; // already touching and closing: contact right now
  const aq = vx * vx + vy * vy;
  const disc = bq * bq - aq * c;
  if (disc < 0) return -1;
  // The smaller root, in the form that stays accurate when c is tiny.
  const t = c / (-bq + Math.sqrt(disc));
  return t <= tMax ? t : -1;
}

/** Earliest contact of a ball centred at p, moving at v, with a cushion piece. */
export function toiSegment(px: number, py: number, vx: number, vy: number, s: Segment, tMax: number): number {
  const vn = vx * s.nx + vy * s.ny;
  if (vn >= 0) return -1;
  const dist = (px - s.ax) * s.nx + (py - s.ay) * s.ny;
  if (dist < -BALL_R * 0.5) return -1; // behind it: unreachable from this side
  let t = (dist - BALL_R) / -vn;
  if (t > tMax) return -1;
  if (t < 0) t = 0;
  const along = (px + vx * t - s.ax) * s.dx + (py + vy * t - s.ay) * s.dy;
  // Beyond either end the rounded jaw or end point takes the hit instead.
  if (along < 0 || along > s.len) return -1;
  return t;
}

const KIND_NONE = 0;
const KIND_BALL = 1;
const KIND_SEGMENT = 2;
const KIND_CIRCLE = 3;

function advance(sim: Sim, h: number): void {
  const balls = sim.balls;
  const nb = balls.length;
  const segments = TABLE.segments;
  const circles = TABLE.circles;
  let remaining = h;

  for (let guard = 0; remaining > 0; guard++) {
    if (guard >= MAX_EVENTS) {
      drift(balls, remaining);
      separateOverlaps(balls);
      break;
    }
    let tMin = remaining;
    let kind = KIND_NONE;
    let ia = -1;
    let ib = -1;

    for (let i = 0; i < nb; i++) {
      const a = balls[i];
      if (a.pocket >= 0) continue;
      const aMoving = a.vx !== 0 || a.vy !== 0;
      for (let j = i + 1; j < nb; j++) {
        const b = balls[j];
        if (b.pocket >= 0) continue;
        if (!aMoving && b.vx === 0 && b.vy === 0) continue;
        const t = toiCircle(a.x, a.y, a.vx - b.vx, a.vy - b.vy, b.x, b.y, BALL_D, tMin);
        if (t >= 0 && t < tMin) {
          tMin = t;
          kind = KIND_BALL;
          ia = i;
          ib = j;
        }
      }
      if (!aMoving) continue;
      // Cushions only line the edges: skip them for a ball that cannot reach
      // one in the time left.
      const reach = BALL_R + Math.sqrt(a.vx * a.vx + a.vy * a.vy) * remaining + 1;
      if (a.x > reach && a.x < TABLE_L - reach && a.y > reach && a.y < TABLE_W - reach) continue;
      for (let s = 0; s < segments.length; s++) {
        const t = toiSegment(a.x, a.y, a.vx, a.vy, segments[s], tMin);
        if (t >= 0 && t < tMin) {
          tMin = t;
          kind = KIND_SEGMENT;
          ia = i;
          ib = s;
        }
      }
      for (let c = 0; c < circles.length; c++) {
        const cc = circles[c];
        const t = toiCircle(a.x, a.y, a.vx, a.vy, cc.x, cc.y, BALL_R + cc.r, tMin);
        if (t >= 0 && t < tMin) {
          tMin = t;
          kind = KIND_CIRCLE;
          ia = i;
          ib = c;
        }
      }
    }

    if (kind === KIND_NONE) {
      drift(balls, remaining);
      remaining = 0;
    } else {
      drift(balls, tMin);
      remaining -= tMin;
      if (kind === KIND_BALL) {
        const cluster = touchingCluster(balls, ia, ib);
        if (cluster.length > 2) resolveCluster(sim, cluster, ia, ib);
        else collideBalls(sim, balls[ia], balls[ib]);
      } else if (kind === KIND_SEGMENT) {
        const s = segments[ib];
        bounce(sim, balls[ia], s.nx, s.ny);
      } else {
        const c = circles[ib];
        const a = balls[ia];
        const dx = a.x - c.x;
        const dy = a.y - c.y;
        const d = Math.hypot(dx, dy) || 1;
        bounce(sim, a, dx / d, dy / d);
      }
    }
    capturePockets(sim);
  }
}

function collideBalls(sim: Sim, a: Ball, b: Ball): void {
  let nx = b.x - a.x;
  let ny = b.y - a.y;
  const d = Math.hypot(nx, ny);
  if (d === 0) {
    nx = 1;
    ny = 0;
  } else {
    nx /= d;
    ny /= d;
  }
  const vn = (a.vx - b.vx) * nx + (a.vy - b.vy) * ny;
  if (vn <= 0) return;
  // Equal masses: swap the normal components, less what restitution keeps.
  // The contact is frictionless, so each ball keeps its own spin; that is
  // what makes a rolling cue ball follow through and a drawn one come back.
  const j = 0.5 * (1 + BALL_RESTITUTION) * vn;
  a.vx -= j * nx;
  a.vy -= j * ny;
  b.vx += j * nx;
  b.vy += j * ny;

  const ev = sim.events;
  if (ev.firstContact === null) {
    if (a.id === 0) ev.firstContact = b.id;
    else if (b.id === 0) ev.firstContact = a.id;
  }
  if (sim.recordImpacts) {
    sim.impacts.push({ kind: "ball", speed: vn, x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 });
  }
}

/* ---------------------------------------------------- touching groups */

/** Balls closer than this to touching count as frozen together. */
const CLUSTER_SLOP = 0.05;
/** Peak overlap of the virtual contact springs at the reference speed. */
const SOFT_PEN = 0.05;
/** Integration steps across one contact at the reference speed. */
const SOFT_STEPS_PER_CONTACT = 60;
/** Contact-free steps that end the virtual run. */
const SOFT_QUIET_STEPS = 120;
const SOFT_MAX_STEPS = 12000;

/** Every ball connected to `ia`/`ib` through chains of touching balls. */
function touchingCluster(balls: Ball[], ia: number, ib: number): number[] {
  const reach2 = (BALL_D + CLUSTER_SLOP) * (BALL_D + CLUSTER_SLOP);
  const members = [ia, ib];
  for (let k = 0; k < members.length; k++) {
    const m = balls[members[k]];
    for (let j = 0; j < balls.length; j++) {
      const o = balls[j];
      if (o.pocket >= 0 || members.includes(j)) continue;
      const dx = o.x - m.x;
      const dy = o.y - m.y;
      if (dx * dx + dy * dy <= reach2) members.push(j);
    }
  }
  return members;
}

/**
 * Resolve a contact inside a group of touching balls — a tight rack, or a
 * combination of frozen balls — by briefly simulating the group as stiff
 * Hertzian springs (F ∝ overlap^1.5, with Hunt–Crossley damping for the
 * restitution). Instantaneous pairwise collisions would have to pick an
 * order, and in a racked triangle any order sends the whole impulse down the
 * two edges like a Newton's cradle; letting every contact push at once
 * spreads the break through the rack the way a real one does.
 *
 * Only the resulting velocities are kept — the balls stay where they touch —
 * so the solve can never push a ball into a cushion or another ball. If the
 * virtual run ever gained energy (it should not), the plain pairwise
 * collision is used instead.
 */
function resolveCluster(sim: Sim, ids: number[], ia: number, ib: number): void {
  const balls = sim.balls;
  const n = ids.length;
  const px = new Float64Array(n);
  const py = new Float64Array(n);
  const vx = new Float64Array(n);
  const vy = new Float64Array(n);
  let energy0 = 0;
  let vRef = 1;
  for (let k = 0; k < n; k++) {
    const b = balls[ids[k]];
    px[k] = b.x;
    py[k] = b.y;
    vx[k] = b.vx;
    vy[k] = b.vy;
    const v2 = b.vx * b.vx + b.vy * b.vy;
    energy0 += v2;
    // Pairs close at up to twice the fastest ball's speed: size the springs
    // and the step to that, whatever direction the fast ball is heading.
    const v = 2 * Math.sqrt(v2);
    if (v > vRef) vRef = v;
  }

  // Overlap each pair starts with (normally none) is not pushed on.
  const rest = new Float64Array(n * n);
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      const d = Math.hypot(px[j] - px[i], py[j] - py[i]);
      rest[i * n + j] = Math.max(0, BALL_D - d);
    }
  }

  const stiffness = (0.625 * vRef * vRef) / Math.pow(SOFT_PEN, 2.5);
  const dt = (3.2 * SOFT_PEN) / vRef / SOFT_STEPS_PER_CONTACT;
  const damping = (3 * (1 - BALL_RESTITUTION)) / (2 * vRef);

  let quiet = 0;
  for (let s = 0; s < SOFT_MAX_STEPS && quiet < SOFT_QUIET_STEPS; s++) {
    let touching = false;
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const dx = px[j] - px[i];
        const dy = py[j] - py[i];
        const d2 = dx * dx + dy * dy;
        if (d2 >= CONTACT2) continue;
        const d = Math.sqrt(d2) || 1e-9;
        const overlap = BALL_D - d - rest[i * n + j];
        if (overlap <= 0) continue;
        const nx = dx / d;
        const ny = dy / d;
        const rate = (vx[i] - vx[j]) * nx + (vy[i] - vy[j]) * ny;
        const f = stiffness * overlap * Math.sqrt(overlap) * (1 + damping * rate);
        if (f <= 0) continue;
        const imp = f * dt;
        vx[i] -= imp * nx;
        vy[i] -= imp * ny;
        vx[j] += imp * nx;
        vy[j] += imp * ny;
        touching = true;
      }
    }
    for (let k = 0; k < n; k++) {
      px[k] += vx[k] * dt;
      py[k] += vy[k] * dt;
    }
    quiet = touching ? 0 : quiet + 1;
  }

  let energy1 = 0;
  for (let k = 0; k < n; k++) energy1 += vx[k] * vx[k] + vy[k] * vy[k];
  const a = balls[ia];
  const b = balls[ib];
  const seedClosing =
    ((a.vx - b.vx) * (b.x - a.x) + (a.vy - b.vy) * (b.y - a.y)) / (Math.hypot(b.x - a.x, b.y - a.y) || 1);
  if (!(energy1 <= energy0 * 1.0001)) {
    collideBalls(sim, a, b);
    return;
  }
  for (let k = 0; k < n; k++) {
    balls[ids[k]].vx = vx[k];
    balls[ids[k]].vy = vy[k];
  }

  // Where the balls really sit, the contact normals differ a hair from the
  // virtual run's: take out any approach that is left, so the sweep does not
  // find the same group touching again at once.
  const reach2 = (BALL_D + CLUSTER_SLOP) * (BALL_D + CLUSTER_SLOP);
  for (let pass = 0; pass < 24; pass++) {
    let fixed = false;
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const p = balls[ids[i]];
        const q = balls[ids[j]];
        const dx = q.x - p.x;
        const dy = q.y - p.y;
        const d2 = dx * dx + dy * dy;
        if (d2 > reach2) continue;
        const d = Math.sqrt(d2) || 1e-9;
        const nx = dx / d;
        const ny = dy / d;
        const closing = (p.vx - q.vx) * nx + (p.vy - q.vy) * ny;
        if (closing <= 1e-9) continue;
        const j2 = closing / 2;
        p.vx -= j2 * nx;
        p.vy -= j2 * ny;
        q.vx += j2 * nx;
        q.vy += j2 * ny;
        fixed = true;
      }
    }
    if (!fixed) break;
  }

  const ev = sim.events;
  if (ev.firstContact === null) {
    if (ia === 0) ev.firstContact = ib;
    else if (ib === 0) ev.firstContact = ia;
  }
  if (sim.recordImpacts) {
    sim.impacts.push({ kind: "ball", speed: Math.abs(seedClosing), x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 });
  }
}

function bounce(sim: Sim, a: Ball, nx: number, ny: number): void {
  const vn = a.vx * nx + a.vy * ny;
  if (vn >= 0) return;
  const tx = -ny;
  const ty = nx;
  const vt = a.vx * tx + a.vy * ty;
  const outN = -CUSHION_RESTITUTION * vn;
  const outT = vt * CUSHION_GRIP;
  a.vx = outN * nx + outT * tx;
  a.vy = outN * ny + outT * ty;
  // Mirror the spin the same way so a rolling ball leaves the cushion rolling
  // rather than skidding sideways along a curve.
  const un = a.ux * nx + a.uy * ny;
  const ut = a.ux * tx + a.uy * ty;
  const uN = -CUSHION_RESTITUTION * un;
  const uT = ut * CUSHION_GRIP;
  a.ux = uN * nx + uT * tx;
  a.uy = uN * ny + uT * ty;

  const ev = sim.events;
  if (ev.firstContact !== null) ev.railsAfterContact += 1;
  if (a.id !== 0 && !ev.objectBallsToRail.includes(a.id)) ev.objectBallsToRail.push(a.id);
  if (sim.recordImpacts) {
    sim.impacts.push({ kind: "cushion", speed: -vn, x: a.x - nx * BALL_R, y: a.y - ny * BALL_R });
  }
}

function capturePockets(sim: Sim): void {
  const pockets = TABLE.pockets;
  for (const b of sim.balls) {
    if (b.pocket >= 0) continue;
    if (b.x > EDGE_ZONE && b.x < TABLE_L - EDGE_ZONE && b.y > EDGE_ZONE && b.y < TABLE_W - EDGE_ZONE) {
      continue;
    }
    let hit = -1;
    for (let p = 0; p < pockets.length; p++) {
      const dx = b.x - pockets[p].x;
      const dy = b.y - pockets[p].y;
      if (dx * dx + dy * dy < pockets[p].r * pockets[p].r) {
        hit = p;
        break;
      }
    }
    // Belt and braces: a centre that has left the cloth can only have gone
    // down a pocket throat, so drop it into the nearest pocket.
    if (hit < 0 && (b.x < -BALL_R || b.x > TABLE_L + BALL_R || b.y < -BALL_R || b.y > TABLE_W + BALL_R)) {
      let best = Infinity;
      for (let p = 0; p < pockets.length; p++) {
        const d = Math.hypot(b.x - pockets[p].x, b.y - pockets[p].y);
        if (d < best) {
          best = d;
          hit = p;
        }
      }
    }
    if (hit >= 0) pocketBall(sim, b, hit);
  }
}

function pocketBall(sim: Sim, b: Ball, pocket: number): void {
  const speed = Math.hypot(b.vx, b.vy);
  b.pocket = pocket;
  b.vx = b.vy = b.ux = b.uy = 0;
  sim.events.pocketed.push({ id: b.id, pocket, t: sim.time });
  if (b.id === 0) sim.events.scratch = true;
  if (sim.recordImpacts) {
    sim.impacts.push({ kind: "pocket", speed, x: b.x, y: b.y, id: b.id, pocket });
  }
}

/* ------------------------------------------------------------ friction */

/**
 * Cloth friction for one sub-step. A sliding ball (centre velocity ≠ roll
 * velocity) feels kinetic friction against the slip: the centre slows by μg
 * and the spin changes 5/2 as fast, so the slip closes at 7/2·μg until the
 * ball rolls. A rolling ball only feels rolling resistance and a little drag.
 */
function applyFriction(b: Ball, h: number): void {
  let vx = b.vx;
  let vy = b.vy;
  let ux = b.ux;
  let uy = b.uy;
  if (vx === 0 && vy === 0 && ux === 0 && uy === 0) return;

  const sx = vx - ux;
  const sy = vy - uy;
  const slip = Math.hypot(sx, sy);
  const dv = SLIDE_DECEL * h;
  if (slip > 3.5 * dv) {
    const kx = sx / slip;
    const ky = sy / slip;
    vx -= dv * kx;
    vy -= dv * ky;
    ux += 2.5 * dv * kx;
    uy += 2.5 * dv * ky;
  } else {
    if (slip > 0) {
      // Grips this step: angular momentum about the contact point is kept.
      vx = (5 * vx + 2 * ux) / 7;
      vy = (5 * vy + 2 * uy) / 7;
    }
    const sp = Math.hypot(vx, vy);
    const next = sp - ROLL_DECEL * h;
    if (next <= STOP_SPEED) {
      b.vx = b.vy = b.ux = b.uy = 0;
      return;
    }
    vx *= next / sp;
    vy *= next / sp;
    ux = vx;
    uy = vy;
  }

  const keep = 1 - DRAG * h;
  vx *= keep;
  vy *= keep;
  ux *= keep;
  uy *= keep;
  if (vx * vx + vy * vy < STOP_SPEED * STOP_SPEED && ux * ux + uy * uy < STOP_SPEED * STOP_SPEED) {
    vx = vy = ux = uy = 0;
  }
  b.vx = vx;
  b.vy = vy;
  b.ux = ux;
  b.uy = uy;
}

/**
 * How far a lone ball travels before it stops when sent at `speed` with a
 * roll velocity of `rollRatio` × speed (0: a struck object ball, which starts
 * out sliding; 1: natural roll). The CPU inverts this to pick its pace.
 */
export function travelDistance(speed: number, rollRatio = 0): number {
  const b: Ball = { id: 0, x: 0, y: 0, vx: speed, vy: 0, ux: speed * rollRatio, uy: 0, pocket: -1 };
  const h = TICK / 4;
  let dist = 0;
  for (let i = 0; i < 400000; i++) {
    if (b.vx === 0 && b.ux === 0) break;
    dist += b.vx * h;
    applyFriction(b, h);
  }
  return dist;
}

/**
 * Speed a cue ball still has after travelling `dist` in a straight line when
 * struck at `power` with `spin` (as in a Shot), or 0 if it stops first. Lets
 * the CPU pace draw and stun shots, which bleed speed while they slide.
 */
export function speedAfter(power: number, spin: number, dist: number): number {
  const speed = powerToSpeed(power);
  const tip = Math.min(1, Math.max(-1, spin)) * MAX_TIP_OFFSET;
  const b: Ball = { id: 0, x: 0, y: 0, vx: speed, vy: 0, ux: 2.5 * tip * speed, uy: 0, pocket: -1 };
  const h = TICK / 2;
  let x = 0;
  for (let i = 0; i < 100000 && x < dist; i++) {
    if (b.vx <= 0) return 0;
    x += b.vx * h;
    applyFriction(b, h);
  }
  return Math.max(0, b.vx);
}

/* -------------------------------------------------------------- safety */

/**
 * Push apart any balls that overlap and lift balls off cushions they sit
 * inside. Only reached if a sub-step ran out of its event budget.
 */
export function separateOverlaps(balls: Ball[]): void {
  for (let i = 0; i < balls.length; i++) {
    const a = balls[i];
    if (a.pocket >= 0) continue;
    for (let j = i + 1; j < balls.length; j++) {
      const b = balls[j];
      if (b.pocket >= 0) continue;
      let dx = b.x - a.x;
      let dy = b.y - a.y;
      let d = Math.hypot(dx, dy);
      if (d >= BALL_D) continue;
      if (d === 0) {
        dx = 1;
        dy = 0;
        d = 1;
      }
      const push = (BALL_D - d) / 2 + 1e-6;
      a.x -= (dx / d) * push;
      a.y -= (dy / d) * push;
      b.x += (dx / d) * push;
      b.y += (dy / d) * push;
    }
    for (const s of TABLE.segments) {
      const dist = (a.x - s.ax) * s.nx + (a.y - s.ay) * s.ny;
      if (dist >= BALL_R || dist < -BALL_R * 0.5) continue;
      const along = (a.x - s.ax) * s.dx + (a.y - s.ay) * s.dy;
      if (along < 0 || along > s.len) continue;
      a.x += s.nx * (BALL_R - dist);
      a.y += s.ny * (BALL_R - dist);
    }
  }
}
