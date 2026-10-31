import { sweep, sweepEmpty } from "./aim";
import {
  BALL_D,
  BALL_RESTITUTION,
  BREAK_SPOT,
  FOOT_SPOT,
  HEAD_STRING_X,
  MAX_SPEED,
  MIN_POWER,
  TABLE_L,
  TABLE_W,
} from "./constants";
import { simulateShot, speedAfter, travelDistance } from "./physics";
import type { Rng } from "./rng";
import { legalTargets, placeCueBall, resolveShot, type GameState } from "./rules";
import { isValidPlacement, nearestFreeSpot, TABLE } from "./table";
import type { Ball, BallInHand, Difficulty, Shot, Vec } from "./types";

/**
 * The computer player.
 *
 * Every tier plays the same way at heart: for each legal ball and each pocket
 * it finds the window of directions that send the ball cleanly into the
 * pocket, aims the cue ball at the matching ghost-ball position, rejects the
 * shot if either path is blocked, and scores what is left by how much aiming
 * error it forgives — which falls with a thinner cut and a longer distance.
 *
 * Easy stops there and shoots with a shaky arm. Medium aims better, plays
 * safe when nothing is on and test-plays its best few shots to dodge
 * scratches. Hard test-plays its top shots with several speeds and spins in
 * the real physics engine, prefers the ones that pot without a foul *and*
 * leave the cue ball well for the next shot, checks that its choice survives
 * small aiming errors, and looks for safeties and kick shots when nothing
 * can be potted.
 *
 * Planning is a generator that yields between simulations, so the view can
 * run it in slices and keep animating while the CPU "thinks".
 */

export interface CpuPlan {
  shot: Shot;
  /** Where to put the cue ball first when it is in hand. */
  place: Vec | null;
  kind: "break" | "pot" | "safety";
  target: number | null;
  pocket: number | null;
}

interface Profile {
  /** Standard deviation of the aiming error, radians. */
  aimSigma: number;
  /** Relative standard deviation of the stroke's power. */
  powerSigma: number;
  /** none: heuristics only · check: test-play the best few · full: search. */
  search: "none" | "check" | "full";
  /** Play safe when the best pot is less likely than this. */
  safetyBelow: number;
  breakPower: number;
  /** Extra pace on pots, so that a miss still carries a ball to a cushion. */
  firmness: number;
}

const DEG = Math.PI / 180;

const PROFILES: Record<Difficulty, Profile> = {
  easy: { aimSigma: 1.5 * DEG, powerSigma: 0.18, search: "none", safetyBelow: 0, breakPower: 0.78, firmness: 1.3 },
  medium: { aimSigma: 0.45 * DEG, powerSigma: 0.07, search: "check", safetyBelow: 0.2, breakPower: 0.9, firmness: 1.12 },
  hard: { aimSigma: 0.1 * DEG, powerSigma: 0.025, search: "full", safetyBelow: 0.3, breakPower: 1, firmness: 1 },
};

/** Aim error used to rank shots by how forgiving they are, whoever shoots. */
const RANK_SIGMA = 0.45 * DEG;
/** Thinner cuts than this are not attempted. */
const MAX_CUT = 78 * DEG;
/** How far past the pocket the object ball is paced to run. */
const OVERRUN = 220;

/* ---------------------------------------------------------------- pace */

interface PaceTable {
  step: number;
  slide: number[];
  roll: number[];
}
let paceCache: PaceTable | null = null;

/** Stopping distance by launch speed, for a sliding start and a rolling one. */
function paceTable(): PaceTable {
  if (!paceCache) {
    const step = 100;
    const slide: number[] = [];
    const roll: number[] = [];
    for (let v = 0; v <= MAX_SPEED * 1.6; v += step) {
      slide.push(travelDistance(v, 0));
      roll.push(travelDistance(v, 1));
    }
    paceCache = { step, slide, roll };
  }
  return paceCache;
}

/** Speed that carries a ball `dist` millimetres. */
function speedFor(dist: number, rolling: boolean): number {
  const t = paceTable();
  const d = rolling ? t.roll : t.slide;
  if (dist <= 0) return 0;
  for (let i = 1; i < d.length; i++) {
    if (d[i] >= dist) {
      const f = (dist - d[i - 1]) / (d[i] - d[i - 1]);
      return (i - 1 + f) * t.step;
    }
  }
  return (d.length - 1) * t.step;
}

/** Distance a ball sent at `speed` covers. */
function distFor(speed: number, rolling: boolean): number {
  const t = paceTable();
  const d = rolling ? t.roll : t.slide;
  const i = Math.min(d.length - 2, Math.max(0, Math.floor(speed / t.step)));
  const f = Math.min(1, Math.max(0, speed / t.step - i));
  return d[i] + (d[i + 1] - d[i]) * f;
}

/* ---------------------------------------------------------------- math */

function gauss(rng: Rng): number {
  const u = Math.max(1e-12, rng.next());
  const v = rng.next();
  return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v);
}

/** Abramowitz–Stegun 7.1.26, plenty for a probability estimate. */
function erf(x: number): number {
  const s = x < 0 ? -1 : 1;
  const a = Math.abs(x);
  const t = 1 / (1 + 0.3275911 * a);
  const y =
    1 - ((((1.061405429 * t - 1.453152027) * t + 1.421413741) * t - 0.284496736) * t + 0.254829592) * t * Math.exp(-a * a);
  return s * y;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/* ------------------------------------------------------------- windows */

export interface PotWindow {
  /** Middle of the window: the object-ball direction to aim for. */
  aim: number;
  /** Half its width, radians. */
  half: number;
}

/**
 * The directions that send a ball at (ox, oy) cleanly into `pocket` on an
 * empty table — no jaw touched on the way. Found by sweeping the ball's path
 * against the real cushion geometry, so the CPU's idea of a pocket is the
 * physics' idea of a pocket.
 */
export function potWindow(ox: number, oy: number, pocket: number): PotWindow | null {
  const p = TABLE.pockets[pocket];
  const base = Math.atan2(p.y - oy, p.x - ox);
  const pots = (a: number) => {
    const h = sweepEmpty(ox, oy, Math.cos(a), Math.sin(a));
    return h.kind === "pocket" && h.id === pocket;
  };
  let seed = Number.NaN;
  for (let k = 0; k <= 24; k++) {
    const off = (k % 2 === 0 ? 1 : -1) * Math.ceil(k / 2) * 0.5 * DEG;
    if (pots(base + off)) {
      seed = base + off;
      break;
    }
  }
  if (Number.isNaN(seed)) return null;
  const edge = (sign: 1 | -1) => {
    let lo = 0;
    let hi = 0;
    for (let s = 0.25 * DEG; s <= 20 * DEG; s += 0.25 * DEG) {
      if (pots(seed + sign * s)) lo = s;
      else {
        hi = s;
        break;
      }
    }
    if (hi === 0) return lo;
    for (let i = 0; i < 6; i++) {
      const mid = (lo + hi) / 2;
      if (pots(seed + sign * mid)) lo = mid;
      else hi = mid;
    }
    return lo;
  };
  const right = edge(1);
  const left = edge(-1);
  return { aim: seed + (right - left) / 2, half: (right + left) / 2 };
}

type Windows = Map<number, PotWindow>;

function computeWindows(balls: readonly Ball[], targets: readonly number[]): Windows {
  const map: Windows = new Map();
  for (const t of targets) {
    for (let p = 0; p < TABLE.pockets.length; p++) {
      const w = potWindow(balls[t].x, balls[t].y, p);
      if (w) map.set(t * 8 + p, w);
    }
  }
  return map;
}

/* ---------------------------------------------------------- candidates */

interface Candidate {
  target: number;
  pocket: number;
  cue: Vec;
  angle: number;
  /** Rough power for a centre-ball stroke; strokes are paced exactly later. */
  power: number;
  /** Cue-ball speed needed at contact, and its distance to the contact. */
  vImpact: number;
  dCG: number;
  cut: number;
  /** Object ball's distance to the pocket. */
  objDist: number;
  /** Rough chance a steady player pots it. */
  value: number;
}

/**
 * A ghost-ball pot of `target` into `pocket` from a cue ball at (cx, cy), or
 * null if it is blocked, too thin or out of reach.
 */
function potCandidate(
  balls: readonly Ball[],
  cx: number,
  cy: number,
  target: number,
  pocket: number,
  win: PotWindow
): Candidate | null {
  const o = balls[target];
  const ux = Math.cos(win.aim);
  const uy = Math.sin(win.aim);
  const gx = o.x - ux * BALL_D;
  const gy = o.y - uy * BALL_D;
  const vx = gx - cx;
  const vy = gy - cy;
  const dCG = Math.hypot(vx, vy);
  if (dCG < 1) return null;
  const dx = vx / dCG;
  const dy = vy / dCG;
  const cosCut = dx * ux + dy * uy;
  if (cosCut < Math.cos(MAX_CUT)) return null;

  // The cue ball must reach the ghost position untouched (and not fall in).
  const h1 = sweep(balls, cx, cy, dx, dy, 0, -1, true);
  if (h1.kind !== "ball" || h1.id !== target || Math.abs(h1.dist - dCG) > 1) return null;
  // The object ball's road to the pocket must be clear of other balls.
  const h2 = sweep(balls, o.x, o.y, ux, uy, target, 0, true);
  if (h2.kind !== "pocket" || h2.id !== pocket) return null;

  // Pace: enough for the object ball to run OVERRUN past the pocket. A
  // centre-ball cue ball behaves like a struck object ball (it slides, then
  // rolls), which is close enough for ranking; strokes get exact power later.
  const vObj = speedFor(h2.dist + OVERRUN, false);
  const vImpact = vObj / (cosCut * (1 + BALL_RESTITUTION) * 0.5);
  const vStart = speedFor(distFor(vImpact, false) + dCG, false);
  const power = Math.max(0.1, vStart / MAX_SPEED);
  if (power > 1) return null;

  // An aiming error δ moves the contact point dCG·δ sideways, which swings
  // the object ball by about δ·dCG / (2R·cos cut): the window it must hit,
  // seen from the cue, shrinks by that factor.
  const tol = win.half / (1 + dCG / (BALL_D * Math.max(cosCut, 0.05)));
  let value = erf(tol / (RANK_SIGMA * Math.SQRT2));
  if (power > 0.8) value *= 1 - (power - 0.8);
  return {
    target,
    pocket,
    cue: { x: cx, y: cy },
    angle: Math.atan2(dy, dx),
    power,
    vImpact,
    dCG,
    cut: Math.acos(clamp(cosCut, -1, 1)),
    objDist: h2.dist,
    value,
  };
}

function candidatesFrom(
  balls: readonly Ball[],
  cx: number,
  cy: number,
  targets: readonly number[],
  windows: Windows
): Candidate[] {
  const out: Candidate[] = [];
  for (const t of targets) {
    for (let p = 0; p < TABLE.pockets.length; p++) {
      const w = windows.get(t * 8 + p);
      if (!w) continue;
      const c = potCandidate(balls, cx, cy, t, p, w);
      if (c) out.push(c);
    }
  }
  return out.sort((a, b) => b.value - a.value);
}

/** The best pot available to whoever is on turn in `state` (0 if none). */
export function bestPotValue(state: GameState): number {
  if (state.winner !== null) return 0;
  if (state.ballInHand !== "none") return 0.95;
  const targets = legalTargets(state);
  const windows = computeWindows(state.balls, targets);
  const cue = state.balls[0];
  const c = candidatesFrom(state.balls, cue.x, cue.y, targets, windows);
  return c.length ? c[0].value : 0;
}

/**
 * A default stroke for a candidate: a touch of draw on short, straight pots
 * (so the cue ball does not follow the object ball in), a touch of follow on
 * the rest.
 */
function defaultSpin(c: Candidate): number {
  if (c.cut < 14 * DEG && c.objDist < 900) return -0.35;
  return 0.12;
}

/**
 * Power that delivers the cue ball to the contact at the candidate's speed
 * with this spin — draw and stun slide longer and need more — times `pace`.
 */
function strokePower(c: Candidate, spin: number, pace: number): number {
  let lo = MIN_POWER;
  let hi = 1;
  if (speedAfter(hi, spin, c.dCG) < c.vImpact) return 1;
  for (let i = 0; i < 14; i++) {
    const mid = (lo + hi) / 2;
    if (speedAfter(mid, spin, c.dCG) >= c.vImpact) hi = mid;
    else lo = mid;
  }
  return clamp(hi * pace, MIN_POWER, 1);
}

/* -------------------------------------------------------- simulation */

/**
 * Test-play a stroke and score where it leaves us: winning or losing the
 * game dominates, a foul is bad, a pot is good and better still with an easy
 * follow-up, and a miss is as bad as the chance it hands the opponent.
 */
function simulateValue(state: GameState, place: Vec | null, shot: Shot): number {
  let s = state;
  if (place) {
    const placed = placeCueBall(state, place.x, place.y);
    if (!placed) return -50;
    s = placed;
  }
  const res = simulateShot(s.balls, shot);
  const next = resolveShot(s, res.events, res.balls);
  const me = state.turn;
  if (next.winner !== null) return next.winner === me ? 100 : -100;
  const out = next.lastOutcome;
  if (!out) return 0;
  if (out.fouls.length > 0) return -3;
  if (out.keepsTurn) return 2 + 1.5 * bestPotValue(next) + 0.05 * out.potted.length;
  return -1.5 * bestPotValue(next);
}

/* ------------------------------------------------------------- options */

interface Option {
  place: Vec | null;
  cand: Candidate;
}

function placementOptions(state: GameState, targets: readonly number[], windows: Windows): Option[] {
  const zone: BallInHand = state.ballInHand;
  const balls = state.balls;
  const best = new Map<string, Option>();
  const consider = (x: number, y: number, only?: { t: number; p: number }) => {
    if (!isValidPlacement(balls, x, y, zone)) return;
    const list = only
      ? (() => {
          const w = windows.get(only.t * 8 + only.p);
          const c = w ? potCandidate(balls, x, y, only.t, only.p, w) : null;
          return c ? [c] : [];
        })()
      : candidatesFrom(balls, x, y, targets, windows);
    for (const c of list) {
      const key = `${c.target}:${c.pocket}`;
      // A small cut beats dead straight: it steers the cue ball off the
      // object ball's line, away from following it into the pocket.
      const shaped = c.value * (c.cut > 6 * DEG && c.cut < 35 * DEG ? 1.04 : 1);
      const prev = best.get(key);
      if (!prev || shaped > prev.cand.value) best.set(key, { place: { x, y }, cand: { ...c, value: shaped } });
    }
  };

  for (const t of targets) {
    for (let p = 0; p < TABLE.pockets.length; p++) {
      const w = windows.get(t * 8 + p);
      if (!w) continue;
      const o = balls[t];
      const gx = o.x - Math.cos(w.aim) * BALL_D;
      const gy = o.y - Math.sin(w.aim) * BALL_D;
      for (const cutDeg of [0, 12, -12, 24, -24]) {
        for (const d of [150, 300, 520]) {
          const a = w.aim + cutDeg * DEG;
          consider(gx - Math.cos(a) * d, gy - Math.sin(a) * d, { t, p });
        }
      }
    }
  }
  if (zone === "kitchen") {
    for (let x = 90; x <= HEAD_STRING_X - 10; x += 110) {
      for (let y = 80; y <= TABLE_W - 80; y += 105) consider(x, y);
    }
  }
  return [...best.values()].sort((a, b) => b.cand.value - a.cand.value);
}

/* ------------------------------------------------------------ strokes */

function withNoise(shot: Shot, prof: Profile, rng: Rng): Shot {
  return {
    angle: shot.angle + gauss(rng) * prof.aimSigma,
    power: clamp(shot.power * (1 + gauss(rng) * prof.powerSigma), MIN_POWER, 1),
    spin: shot.spin,
  };
}

function breakPlan(state: GameState, prof: Profile, rng: Rng): CpuPlan {
  const y = TABLE_W / 2 + (rng.next() - 0.5) * 300;
  const place = nearestFreeSpot(state.balls, BREAK_SPOT.x, y, "kitchen");
  // A hair off the apex's centre spreads the rack more evenly.
  const off = (rng.next() < 0.5 ? -1 : 1) * (2 + rng.next() * 5);
  const angle = Math.atan2(FOOT_SPOT.y + off - place.y, FOOT_SPOT.x - place.x);
  return {
    shot: withNoise({ angle, power: prof.breakPower, spin: 0.15 }, { ...prof, aimSigma: prof.aimSigma * 0.5 }, rng),
    place,
    kind: "break",
    target: null,
    pocket: null,
  };
}

/**
 * Direct strokes at legal balls the cue ball can see, for safeties: full,
 * half and thin contacts at three paces, each paced from the distance so the
 * cue ball arrives with enough left to send something to a cushion.
 */
function safetyStrokes(state: GameState, from: Vec, targets: readonly number[], limit: number): Shot[] {
  const balls = state.balls;
  const visible = targets
    .map((t) => ({ t, d: Math.hypot(balls[t].x - from.x, balls[t].y - from.y) }))
    .sort((a, b) => a.d - b.d);
  const shots: Shot[] = [];
  for (const { t, d } of visible) {
    const base = Math.atan2(balls[t].y - from.y, balls[t].x - from.x);
    const pace = speedFor(d + 380, false) / MAX_SPEED;
    for (const frac of [0, 0.5, -0.5, 0.82, -0.82]) {
      const a = base + Math.asin(clamp((frac * BALL_D) / Math.max(d, BALL_D), -1, 1));
      const h = sweep(balls, from.x, from.y, Math.cos(a), Math.sin(a), 0, -1, true);
      if (h.kind !== "ball" || h.id !== t) continue;
      for (const k of [1.35, 1, 1.8]) shots.push({ angle: a, power: clamp(pace * k, 0.12, 1), spin: 0 });
    }
    if (shots.length >= limit) break;
  }
  return shots.slice(0, limit);
}

/** Every direction around the cue ball, for kicks when nothing is in sight. */
function kickStrokes(count: number, powers: readonly number[]): Shot[] {
  const shots: Shot[] = [];
  for (let i = 0; i < count; i++) {
    for (const power of powers) shots.push({ angle: (i / count) * Math.PI * 2, power, spin: 0 });
  }
  return shots;
}

/* ---------------------------------------------------------------- plan */

/**
 * Plan the CPU's next stroke. Yields between expensive steps; the returned
 * plan already includes the tier's aiming and power error.
 */
export function* planShot(state: GameState, difficulty: Difficulty, rng: Rng): Generator<void, CpuPlan, void> {
  const prof = PROFILES[difficulty];
  if (state.isBreak) return breakPlan(state, prof, rng);

  const targets = legalTargets(state);
  const windows = computeWindows(state.balls, targets);
  yield;

  const inHand = state.ballInHand !== "none";
  let options: Option[];
  if (inHand) {
    options = placementOptions(state, targets, windows);
  } else {
    const cue = state.balls[0];
    options = candidatesFrom(state.balls, cue.x, cue.y, targets, windows).map((cand) => ({ place: null, cand }));
  }
  yield;

  const potPlan = (o: Option, shot: Shot): CpuPlan => ({
    shot: withNoise(shot, prof, rng),
    place: o.place,
    kind: "pot",
    target: o.cand.target,
    pocket: o.cand.pocket,
  });
  const stroke = (o: Option, spin = defaultSpin(o.cand), pace = 1): Shot => ({
    angle: o.cand.angle,
    power: strokePower(o.cand, spin, pace * prof.firmness),
    spin,
  });

  // Where the cue ball will be for a safety: put it somewhere sensible first.
  const safetyPlace: Vec | null = inHand
    ? options[0]?.place ?? nearestFreeSpot(state.balls, TABLE_L / 4, TABLE_W / 2, state.ballInHand)
    : null;
  const safetyFrom: Vec = safetyPlace ?? { x: state.balls[0].x, y: state.balls[0].y };

  /* Easy: pick one of the better pots and hope. */
  if (prof.search === "none") {
    if (options.length > 0) {
      const pick = options[Math.min(options.length - 1, Math.floor(rng.next() * Math.min(3, options.length)))];
      return potPlan(pick, stroke(pick));
    }
    const shots = safetyStrokes(state, safetyFrom, targets, 3);
    const shot = shots[0] ?? kickStrokes(1, [0.35]).map((s) => ({ ...s, angle: rng.next() * Math.PI * 2 }))[0];
    return { shot: withNoise(shot, prof, rng), place: safetyPlace, kind: "safety", target: null, pocket: null };
  }

  /* Medium: the best pot that test-plays without a foul, else a safety. */
  if (prof.search === "check") {
    if (options.length > 0 && options[0].cand.value >= prof.safetyBelow) {
      for (const o of options.slice(0, 3)) {
        const shot = stroke(o);
        const v = simulateValue(state, o.place, shot);
        yield;
        if (v > -3) return potPlan(o, shot);
      }
    }
    let best: { shot: Shot; v: number } | null = null;
    for (const shot of safetyStrokes(state, safetyFrom, targets, 18)) {
      const v = simulateValue(state, safetyPlace, shot);
      yield;
      if (!best || v > best.v) best = { shot, v };
    }
    if (!best || best.v <= -3) {
      for (const shot of kickStrokes(72, [0.35, 0.55])) {
        const v = simulateValue(state, safetyPlace, shot);
        yield;
        if (!best || v > best.v) best = { shot, v };
      }
    }
    if (options.length > 0 && (!best || best.v <= -3)) return potPlan(options[0], stroke(options[0]));
    return {
      shot: withNoise(best ? best.shot : kickStrokes(1, [0.4])[0], prof, rng),
      place: safetyPlace,
      kind: "safety",
      target: null,
      pocket: null,
    };
  }

  /* Hard: search speeds and spins on the top pots, then safeties. */
  interface Trial {
    option: Option | null;
    place: Vec | null;
    shot: Shot;
    v: number;
  }
  const trials: Trial[] = [];
  const top = options.slice(0, inHand ? 5 : 6);
  for (const o of top) {
    for (const spin of [defaultSpin(o.cand), -0.5, 0.4]) {
      for (const pace of [1, 1.35]) {
        const shot = stroke(o, spin, pace);
        trials.push({ option: o, place: o.place, shot, v: simulateValue(state, o.place, shot) });
        yield;
      }
    }
  }
  trials.sort((a, b) => b.v - a.v);

  // Nothing convincing to pot: weigh safeties (or kicks, when no legal ball
  // is in sight) by the chance they leave the opponent.
  if (trials.length === 0 || trials[0].v < 2 + prof.safetyBelow) {
    for (const shot of safetyStrokes(state, safetyFrom, targets, 45)) {
      trials.push({ option: null, place: safetyPlace, shot, v: simulateValue(state, safetyPlace, shot) });
      yield;
    }
    trials.sort((a, b) => b.v - a.v);
    if (trials.length === 0 || trials[0].v <= -3) {
      for (const shot of kickStrokes(72, [0.3, 0.5])) {
        trials.push({ option: null, place: safetyPlace, shot, v: simulateValue(state, safetyPlace, shot) });
        yield;
      }
      trials.sort((a, b) => b.v - a.v);
    }
  }
  if (trials.length === 0) {
    return { shot: withNoise(kickStrokes(1, [0.4])[0], prof, rng), place: safetyPlace, kind: "safety", target: null, pocket: null };
  }

  // Prefer strokes that still work when the arm wobbles a little, in aim
  // and in pace.
  const robust: Trial[] = [];
  for (const t of trials.slice(0, 3)) {
    let sum = t.v;
    for (const k of [-1.6, 1.6]) {
      sum += simulateValue(state, t.place, { ...t.shot, angle: t.shot.angle + k * prof.aimSigma });
      sum += simulateValue(state, t.place, {
        ...t.shot,
        power: clamp(t.shot.power * (1 + k * prof.powerSigma), MIN_POWER, 1),
      });
    }
    robust.push({ ...t, v: sum / 5 });
    yield;
  }
  robust.sort((a, b) => b.v - a.v);
  const chosen = robust[0];
  if (chosen.option) return potPlan(chosen.option, chosen.shot);
  return { shot: withNoise(chosen.shot, prof, rng), place: chosen.place, kind: "safety", target: null, pocket: null };
}

/** Run the planner to completion in one go (tests and fallbacks). */
export function chooseShot(state: GameState, difficulty: Difficulty, rng: Rng): CpuPlan {
  const gen = planShot(state, difficulty, rng);
  for (;;) {
    const r = gen.next();
    if (r.done) return r.value;
  }
}
