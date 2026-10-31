import { sweep } from "./game/aim";
import { BALL_R, MIN_POWER, TICK } from "./game/constants";
import { planShot, type CpuPlan } from "./game/cpu";
import { createSim, step, strike, type Sim } from "./game/physics";
import { makeRng } from "./game/rng";
import { foulText, legalTargets, newGame, other, placeCueBall, resolveShot, type GameState } from "./game/rules";
import { clampToZone, isFree, nearestFreeSpot } from "./game/table";
import type { Ball, Difficulty, Mode, PlayerIndex, Shot, Vec } from "./game/types";

/*
 * One match, from the break to the 8: the live table, whose turn it is, what
 * the player is doing with the cue, and the CPU's thinking and cueing. It has
 * no React and no DOM — the view calls `frame(now)` every animation frame and
 * forwards input as intents — so the same object drives the canvas, the HUD
 * and the controls, and time only moves when the view says so (a hidden tab
 * simply stops calling it).
 */

export type Phase = "aim" | "strike" | "rolling" | "cpu-think" | "cpu-place" | "cpu-aim" | "over";

export type Voice = "cue" | "click" | "cushion" | "pocket" | "foul" | "turn" | "group" | "win" | "lose" | "place";

export interface Toast {
  id: number;
  title: string;
  detail: string;
  tone: "info" | "good" | "bad";
}

/** What the React side renders; replaced (never mutated) when it changes. */
export interface MatchView {
  phase: Phase;
  game: GameState;
  power: number;
  spin: number;
  toast: Toast | null;
  /** Object balls that have dropped so far, including during the current shot. */
  tray: number[];
  /** The person at the screen may act (aim, place, shoot) right now. */
  humanTurn: boolean;
  thinking: boolean;
  dragging: boolean;
  charging: boolean;
}

export interface MatchOptions {
  mode: Mode;
  difficulty: Difficulty;
  seed: number;
  breaker: PlayerIndex;
  names: [string, string];
  /** Milliseconds from any fixed origin; the CPU slices its thinking by it. */
  clock: () => number;
}

/** Where the match reports sounds and the end of the game. */
export interface MatchHandlers {
  sound: (voice: Voice, magnitude: number) => void;
  over: (state: GameState) => void;
}

/** A ball falling into a pocket, for the drop animation. */
export interface Drop {
  id: number;
  x: number;
  y: number;
  pocket: number;
  t0: number;
}

export interface PocketFlash {
  pocket: number;
  t0: number;
  id: number;
}

/** Pull-back distance of the cue at full power, mm (drawing only). */
export const MAX_PULL = 260;

const THINK_SLICE_MS = 9;
const MIN_THINK_MS = 520;
const MAX_TICKS_PER_FRAME = 12;

const easeInOut = (t: number) => (t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2);

function shortestTurn(from: number, to: number): number {
  let d = to - from;
  while (d > Math.PI) d -= Math.PI * 2;
  while (d < -Math.PI) d += Math.PI * 2;
  return d;
}

export class Match {
  readonly opts: MatchOptions;
  game: GameState;
  sim: Sim | null = null;
  phase: Phase = "aim";

  /** Direction the cue ball would be sent, radians. */
  aim = 0;
  /** Stroke power 0..1 as set on the meter, or as charged. */
  power = 0.5;
  /** -1 draw .. 1 follow. */
  spin = 0;
  /** The person's own settings, restored after the CPU has used the cue. */
  private humanPower = 0.5;
  private humanSpin = 0;
  /** How far the cue is drawn back right now, 0..1 of MAX_PULL. */
  pull = 0;
  /** Opacity of the cue stick, for fading it out once the balls roll. */
  stickAlpha = 1;
  charging = false;
  private chargeSource: "pointer" | "key" | "meter" | null = null;
  private keyChargeT = 0;
  /** Held arrow key: -1, 0, 1 and whether Shift was down. */
  private rotateDir = 0;
  private rotateFine = false;

  /** Where the dragged (or CPU-moved) cue ball is drawn while in hand. */
  cueAt: Vec | null = null;
  dragging = false;
  dragValid = true;

  drops: Drop[] = [];
  flashes: PocketFlash[] = [];
  /** Balls dropped during the shot in progress. */
  private shotTray: number[] = [];

  private shot: Shot | null = null;
  private planner: Generator<void, CpuPlan, void> | null = null;
  private plan: CpuPlan | null = null;
  private phaseT0 = 0;
  private fromAngle = 0;
  private fromCue: Vec | null = null;

  now = 0;
  private last = -1;
  private acc = 0;
  private toast: Toast | null = null;
  private toastSeq = 0;
  private toastUntil = 0;
  private view: MatchView;
  private listeners = new Set<() => void>();

  private handlers: MatchHandlers = { sound: () => {}, over: () => {} };
  private started = false;
  private reducedMotion = false;

  constructor(opts: MatchOptions) {
    this.opts = opts;
    this.game = newGame(opts.seed, opts.breaker);
    this.view = this.makeView();
  }

  /* ------------------------------------------------------- store glue */

  connect(handlers: MatchHandlers): void {
    this.handlers = handlers;
  }

  /** Shorten the CPU's cueing animations for people who prefer less motion. */
  setReducedMotion(on: boolean): void {
    this.reducedMotion = on;
  }

  private onSound(voice: Voice, magnitude: number): void {
    this.handlers.sound(voice, magnitude);
  }

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  getView = (): MatchView => this.view;

  private makeView(): MatchView {
    return {
      phase: this.phase,
      game: this.game,
      power: this.power,
      spin: this.spin,
      toast: this.toast,
      tray: this.shotTray.length ? [...this.game.tray, ...this.shotTray] : this.game.tray,
      humanTurn: this.isHumanTurn() && this.phase === "aim",
      thinking: this.phase === "cpu-think",
      dragging: this.dragging,
      charging: this.charging,
    };
  }

  private publish(): void {
    this.view = this.makeView();
    this.listeners.forEach((fn) => fn());
  }

  /* ---------------------------------------------------------- queries */

  isCpu(player: PlayerIndex): boolean {
    return this.opts.mode === "cpu" && player === 1;
  }

  isHumanTurn(): boolean {
    return this.game.winner === null && !this.isCpu(this.game.turn);
  }

  /** The person may aim and shoot. */
  canAim(): boolean {
    return this.phase === "aim" && this.isHumanTurn() && !this.dragging;
  }

  /** The person may pick up and move the cue ball. */
  canPlace(): boolean {
    return this.phase === "aim" && this.isHumanTurn() && this.game.ballInHand !== "none" && !this.charging;
  }

  /** Ball positions to draw this frame. */
  balls(): readonly Ball[] {
    return this.sim ? this.sim.balls : this.game.balls;
  }

  /** Where the cue ball is drawn (it may be in someone's hand). */
  cuePosition(): Vec {
    if (this.cueAt) return this.cueAt;
    const c = this.balls()[0];
    return { x: c.x, y: c.y };
  }

  /** Whether the cue stick should be drawn. */
  showCue(): boolean {
    if (this.dragging || this.game.winner !== null) return false;
    switch (this.phase) {
      case "aim":
      case "strike":
      case "cpu-think":
      case "cpu-aim":
        return true;
      case "rolling":
        return this.stickAlpha > 0.01;
      default:
        return false;
    }
  }

  /** Whether the aiming guide should be drawn. */
  showGuide(): boolean {
    return !this.dragging && (this.phase === "aim" || this.phase === "cpu-aim");
  }

  /* ------------------------------------------------------------ input */

  /** Point the cue ball toward a table position. */
  aimAt(x: number, y: number): void {
    if (!this.canAim() || this.charging) return;
    const c = this.cuePosition();
    if (Math.hypot(x - c.x, y - c.y) < 2) return;
    this.aim = Math.atan2(y - c.y, x - c.x);
  }

  nudgeAim(delta: number): void {
    if (!this.canAim()) return;
    this.aim += delta;
  }

  /** Arrow keys held: rotate continuously in `frame`. */
  setRotation(dir: number, fine: boolean): void {
    this.rotateDir = dir;
    this.rotateFine = fine;
  }

  setPower(p: number): void {
    const v = Math.min(1, Math.max(0, p));
    if (Math.abs(v - this.power) < 1e-4) return;
    this.power = v;
    this.humanPower = v;
    if (!this.charging) this.pull = 0;
    this.publish();
  }

  setSpin(s: number): void {
    const v = Math.min(1, Math.max(-1, s));
    if (v === this.spin) return;
    this.spin = v;
    this.humanSpin = v;
    this.publish();
  }

  /** Begin drawing the cue back (mouse pull, the power meter or Space). */
  startCharge(source: "pointer" | "key" | "meter"): boolean {
    if (!this.canAim() || this.charging) return false;
    this.charging = true;
    this.chargeSource = source;
    this.keyChargeT = 0;
    if (source !== "meter") {
      this.power = 0;
      this.pull = 0;
    }
    this.publish();
    return true;
  }

  /** Charge level for pointer and meter charging, 0..1. */
  setCharge(p: number): void {
    if (!this.charging) return;
    this.power = Math.min(1, Math.max(0, p));
    this.pull = this.power;
    this.publish();
  }

  /** Let go: shoot if there is power behind it, otherwise put the cue down. */
  releaseCharge(): void {
    if (!this.charging) return;
    this.charging = false;
    this.chargeSource = null;
    if (this.power >= MIN_POWER * 1.5) this.shoot();
    else {
      this.pull = 0;
      this.publish();
    }
  }

  cancelCharge(): void {
    if (!this.charging) return;
    this.charging = false;
    this.chargeSource = null;
    this.pull = 0;
    this.publish();
  }

  /** Strike with the current aim, power and spin. */
  shoot(): void {
    if (!this.canAim()) return;
    this.charging = false;
    const shot: Shot = { angle: this.aim, power: Math.max(MIN_POWER, this.power), spin: this.spin };
    this.beginStrike(shot);
  }

  /** Is (x, y) close enough to the cue ball to pick it up? */
  nearCueBall(x: number, y: number, slack = 2.4): boolean {
    const c = this.cuePosition();
    return Math.hypot(x - c.x, y - c.y) <= BALL_R * slack;
  }

  grabCueBall(): boolean {
    if (!this.canPlace()) return false;
    this.dragging = true;
    this.dragValid = true;
    this.cueAt = this.cuePosition();
    this.publish();
    return true;
  }

  /** Move the cue ball in hand toward (x, y), within its zone. */
  dragCueBall(x: number, y: number): void {
    if (!this.dragging) return;
    const p = clampToZone(x, y, this.game.ballInHand);
    this.cueAt = p;
    this.dragValid = isFree(this.game.balls, p.x, p.y, 0);
  }

  /** Put the cue ball down where it is, or at the nearest free spot. */
  dropCueBall(): void {
    if (!this.dragging || !this.cueAt) return;
    let p = this.cueAt;
    if (!this.dragValid) p = nearestFreeSpot(this.game.balls, p.x, p.y, this.game.ballInHand);
    const placed = placeCueBall(this.game, p.x, p.y);
    if (placed) this.game = placed;
    this.dragging = false;
    this.cueAt = null;
    this.onSound("place", 0.6);
    this.publish();
  }

  /** Nudge the cue ball in hand with the keyboard. */
  moveCueBall(dx: number, dy: number): void {
    if (!this.canPlace()) return;
    const c = this.cuePosition();
    const p = clampToZone(c.x + dx, c.y + dy, this.game.ballInHand);
    if (!isFree(this.game.balls, p.x, p.y, 0)) return;
    const placed = placeCueBall(this.game, p.x, p.y);
    if (placed) {
      this.game = placed;
      this.publish();
    }
  }

  /* -------------------------------------------------------- the clock */

  /** Advance everything to `now` (ms). Call once per animation frame. */
  frame(now: number): void {
    if (this.last < 0) {
      // Anything stamped before the first frame was stamped at 0.
      this.last = now;
      this.phaseT0 += now;
      this.toastUntil += now;
    }
    // A long gap (hidden tab, breakpoint) is not replayed: cap the step.
    const dt = Math.min(100, Math.max(0, now - this.last));
    this.last = now;
    this.now = now;

    if (this.rotateDir !== 0 && this.canAim() && !this.charging) {
      const speed = this.rotateFine ? 0.05 : 0.6; // rad/s
      this.aim += this.rotateDir * speed * (dt / 1000);
    }
    if (this.charging && this.chargeSource === "key") {
      // Space held: the power swings up and back down until released.
      this.keyChargeT += dt / 1300;
      const phase = this.keyChargeT % 2;
      this.power = phase <= 1 ? phase : 2 - phase;
      this.pull = this.power;
      this.publish();
    }

    switch (this.phase) {
      case "strike":
        this.runStrike();
        break;
      case "rolling":
        this.runRolling(dt);
        break;
      case "cpu-think":
        this.runThinking();
        break;
      case "cpu-place":
        this.runCpuPlace();
        break;
      case "cpu-aim":
        this.runCpuAim();
        break;
      default:
        break;
    }

    if (this.phase === "rolling" && this.stickAlpha > 0) {
      this.stickAlpha = Math.max(0, this.stickAlpha - dt / 260);
    }
    if (this.toast && now > this.toastUntil) {
      this.toast = null;
      this.publish();
    }
    this.drops = this.drops.filter((d) => now - d.t0 < 600);
    this.flashes = this.flashes.filter((f) => now - f.t0 < 900);
  }

  /* ----------------------------------------------------------- phases */

  /** Start the match: the breaker's first turn. Safe to call twice. */
  start(): void {
    if (this.started) return;
    this.started = true;
    this.beginTurn(true);
  }

  private setPhase(phase: Phase): void {
    this.phase = phase;
    this.phaseT0 = this.now;
  }

  private showToast(title: string, detail: string, tone: Toast["tone"]): void {
    this.toast = { id: ++this.toastSeq, title, detail, tone };
    this.toastUntil = this.now + 3400;
  }

  private name(p: PlayerIndex): string {
    return this.opts.names[p];
  }

  /** "You keep" / "CPU keeps": the person is addressed in second person. */
  private verb(p: PlayerIndex, base: string, third: string): string {
    return this.opts.mode === "cpu" && p === 0 ? base : third;
  }

  private beginTurn(first = false): void {
    this.shot = null;
    this.plan = null;
    this.planner = null;
    this.pull = 0;
    this.stickAlpha = 1;
    this.charging = false;
    this.dragging = false;
    this.cueAt = null;
    const g = this.game;

    if (g.winner !== null) {
      this.setPhase("over");
      this.publish();
      this.handlers.over(g);
      return;
    }

    if (first) {
      const who = this.name(g.turn);
      this.showToast(
        this.isCpu(g.turn) ? "CPU breaks" : who === "You" ? "Your break" : `${who} breaks`,
        this.isCpu(g.turn) ? "" : "Place the cue ball behind the line, then smash the rack",
        "info"
      );
    }

    if (this.isCpu(g.turn)) {
      this.planner = planShot(g, this.opts.difficulty, makeRng(`${this.opts.seed}:${g.shotNumber}`));
      this.setPhase("cpu-think");
    } else {
      this.aim = this.defaultAim();
      this.power = this.humanPower;
      this.spin = this.humanSpin;
      this.setPhase("aim");
    }
    this.publish();
  }

  /** At the start of a turn, point at the nearest ball the shooter may hit. */
  private defaultAim(): number {
    const g = this.game;
    const cue = g.balls[0];
    let best = this.aim;
    let bestD = Infinity;
    for (const id of legalTargets(g)) {
      const b = g.balls[id];
      const d = Math.hypot(b.x - cue.x, b.y - cue.y);
      const a = Math.atan2(b.y - cue.y, b.x - cue.x);
      const hit = sweep(g.balls, cue.x, cue.y, Math.cos(a), Math.sin(a), 0, -1, false);
      const visible = hit.kind === "ball" && hit.id === id;
      const score = d + (visible ? 0 : 5000);
      if (score < bestD) {
        bestD = score;
        best = a;
      }
    }
    return best;
  }

  private beginStrike(shot: Shot): void {
    this.shot = shot;
    this.aim = shot.angle;
    this.power = shot.power;
    this.pull = Math.max(this.pull, shot.power);
    this.setPhase("strike");
    this.publish();
  }

  /** The cue slides through to the ball, then the balls start to roll. */
  private runStrike(): void {
    const dur = this.reducedMotion ? 40 : 90;
    const t = Math.min(1, (this.now - this.phaseT0) / dur);
    this.pull = (1 - t) * (this.shot?.power ?? 0);
    if (t < 1 || !this.shot) return;
    const sim = createSim(this.game.balls, true);
    strike(sim, this.shot);
    this.sim = sim;
    this.acc = 0;
    this.shotTray = [];
    this.onSound("cue", this.shot.power);
    this.setPhase("rolling");
    this.publish();
  }

  private runRolling(dt: number): void {
    const sim = this.sim;
    if (!sim) return;
    this.acc += dt / 1000;
    let ticks = 0;
    while (this.acc >= TICK && ticks < MAX_TICKS_PER_FRAME && !sim.settled) {
      step(sim);
      this.acc -= TICK;
      ticks += 1;
    }
    if (ticks === MAX_TICKS_PER_FRAME) this.acc = 0;

    let trayChanged = false;
    let clicks = 0;
    for (const imp of sim.impacts) {
      const mag = Math.min(1, imp.speed / 4200);
      if (imp.kind === "ball") {
        if (clicks++ < 4) this.onSound("click", mag);
      } else if (imp.kind === "cushion") {
        if (imp.speed > 40) this.onSound("cushion", mag);
      } else if (imp.kind === "pocket" && imp.id !== undefined && imp.pocket !== undefined) {
        this.onSound("pocket", Math.max(0.35, mag));
        this.drops.push({ id: imp.id, x: imp.x, y: imp.y, pocket: imp.pocket, t0: this.now });
        this.flashes.push({ pocket: imp.pocket, t0: this.now, id: imp.id });
        if (imp.id !== 0) {
          this.shotTray.push(imp.id);
          trayChanged = true;
        }
      }
    }
    sim.impacts.length = 0;
    if (trayChanged) this.publish();
    if (sim.settled) this.finishShot();
  }

  private finishShot(): void {
    const sim = this.sim;
    if (!sim) return;
    const before = this.game;
    const next = resolveShot(before, sim.events, sim.balls);
    this.game = next;
    this.sim = null;
    this.shotTray = [];
    this.announce(before, next);
    this.beginTurn();
  }

  /** Pick the toast and sound for a finished shot. */
  private announce(before: GameState, next: GameState): void {
    const o = next.lastOutcome;
    if (!o) return;
    if (next.winner !== null) {
      this.toast = null;
      const humanWon = this.opts.mode === "local" || next.winner === 0;
      this.onSound(humanWon ? "win" : "lose", 1);
      return;
    }
    const nextName = this.name(next.turn);
    if (o.fouls.length > 0) {
      const scratch = o.fouls.includes("scratch");
      const where = next.ballInHand === "kitchen" ? "behind the line" : "anywhere";
      this.showToast(
        scratch ? "Scratch — ball in hand" : "Foul — ball in hand",
        `${scratch ? "Cue ball down" : foulText(o.fouls)} · ${nextName} ${this.verb(next.turn, "place", "places")} it ${where}`,
        "bad"
      );
      this.onSound("foul", 1);
      return;
    }
    if (o.assigned) {
      const shooter = this.name(o.shooter);
      this.showToast(
        o.assigned === "solids" ? "Solids" : "Stripes",
        `${shooter} ${this.verb(o.shooter, "are", "is")} on ${o.assigned} · ${this.name(other(o.shooter))} ${this.verb(other(o.shooter), "take", "takes")} ${o.assigned === "solids" ? "stripes" : "solids"}`,
        "good"
      );
      this.onSound("group", 1);
      return;
    }
    if (o.respottedEight) {
      this.showToast("8-ball spotted", "It dropped on the break and goes back on the foot spot", "info");
      this.onSound("turn", 0.6);
      return;
    }
    if (o.keepsTurn) {
      const cleared = next.groups[next.turn] !== null && legalTargets(next)[0] === 8;
      this.showToast(
        cleared ? "On the 8" : "Good pot",
        cleared ? `${nextName} ${this.verb(next.turn, "need", "needs")} the 8-ball to win` : `${nextName} ${this.verb(next.turn, "keep", "keeps")} shooting`,
        "good"
      );
      return;
    }
    if (before.turn !== next.turn) {
      const mine = this.opts.mode === "cpu" && next.turn === 0;
      this.showToast(mine ? "Your turn" : `${nextName}'s turn`, o.wasBreak ? "The table is open" : "", "info");
      this.onSound("turn", 0.5);
    }
  }

  /* --------------------------------------------------------------- CPU */

  private runThinking(): void {
    const gen = this.planner;
    if (!gen) return;
    if (!this.plan) {
      const t0 = this.opts.clock();
      for (;;) {
        const r = gen.next();
        if (r.done) {
          this.plan = r.value;
          break;
        }
        if (this.opts.clock() - t0 > THINK_SLICE_MS) return;
      }
    }
    if (this.now - this.phaseT0 < (this.reducedMotion ? 200 : MIN_THINK_MS)) return;
    const plan = this.plan;
    this.planner = null;
    this.fromAngle = this.aim;
    if (plan.place && this.game.ballInHand !== "none") {
      this.fromCue = this.cuePosition();
      this.setPhase("cpu-place");
    } else {
      this.setPhase("cpu-aim");
    }
    this.publish();
  }

  private runCpuPlace(): void {
    const plan = this.plan;
    if (!plan?.place || !this.fromCue) {
      this.setPhase("cpu-aim");
      return;
    }
    const dur = this.reducedMotion ? 120 : 420;
    const t = easeInOut(Math.min(1, (this.now - this.phaseT0) / dur));
    this.cueAt = {
      x: this.fromCue.x + (plan.place.x - this.fromCue.x) * t,
      y: this.fromCue.y + (plan.place.y - this.fromCue.y) * t,
    };
    if (t < 1) return;
    const placed = placeCueBall(this.game, plan.place.x, plan.place.y);
    if (placed) this.game = placed;
    this.cueAt = null;
    this.fromCue = null;
    this.onSound("place", 0.5);
    this.setPhase("cpu-aim");
    this.publish();
  }

  private runCpuAim(): void {
    const plan = this.plan;
    if (!plan) return;
    const turn = shortestTurn(this.fromAngle, plan.shot.angle);
    const rm = this.reducedMotion;
    const turnMs = rm ? 120 : 280 + 420 * Math.min(1, Math.abs(turn) / Math.PI);
    const pullMs = rm ? 120 : 480;
    const holdMs = rm ? 40 : 160;
    const t = this.now - this.phaseT0;
    if (t < turnMs) {
      this.aim = this.fromAngle + turn * easeInOut(t / turnMs);
      return;
    }
    this.aim = plan.shot.angle;
    if (t < turnMs + pullMs) {
      this.pull = plan.shot.power * easeInOut((t - turnMs) / pullMs);
      return;
    }
    if (t < turnMs + pullMs + holdMs) return;
    this.spin = plan.shot.spin;
    this.beginStrike(plan.shot);
  }
}

