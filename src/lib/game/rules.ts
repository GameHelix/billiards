import { BREAK_SPOT, HEAD_SPOT } from "./constants";
import { makeRng } from "./rng";
import { cloneBalls, isSolid, isStripe, isValidPlacement, nearestFreeSpot, rackBalls, spotBall } from "./table";
import type {
  Ball,
  BallInHand,
  EndReason,
  Foul,
  Group,
  PlayerIndex,
  ShotEvents,
} from "./types";

/*
 * Simplified WPA 8-ball. The break is followed by an open table; the first
 * object ball legally pocketed after the break gives its shooter that group.
 * A shooter keeps the table after legally pocketing one of their own balls.
 * Fouls (scratch, no contact, wrong ball first, no ball pocketed and no rail
 * after contact) hand the opponent the cue ball in hand. The 8 wins only when
 * pocketed legally after the shooter's group is cleared; pocketing it early or
 * on a foul loses. No pocket needs to be called. Pocketed balls stay down,
 * except an 8 dropped on the break, which is spotted again.
 */

export interface PlayerStats {
  shots: number;
  /** Object balls this player pocketed (either group). */
  potted: number;
  fouls: number;
}

export interface ShotOutcome {
  shooter: PlayerIndex;
  wasBreak: boolean;
  fouls: Foul[];
  /** Object balls pocketed on the shot, in the order they dropped. */
  potted: number[];
  /** Group the shooter took on this shot, if the table was open. */
  assigned: Group | null;
  keepsTurn: boolean;
  respottedEight: boolean;
  winner: PlayerIndex | null;
  endReason: EndReason | null;
}

export interface GameState {
  /** Resting positions, indexed by ball number. */
  balls: Ball[];
  turn: PlayerIndex;
  /** Both null while the table is open; then set together. */
  groups: [Group | null, Group | null];
  isBreak: boolean;
  ballInHand: BallInHand;
  winner: PlayerIndex | null;
  endReason: EndReason | null;
  shotNumber: number;
  stats: [PlayerStats, PlayerStats];
  /** Every object ball pocketed so far, in order, for the tray. */
  tray: number[];
  lastOutcome: ShotOutcome | null;
}

const emptyStats = (): PlayerStats => ({ shots: 0, potted: 0, fouls: 0 });

/** A fresh racked game; `breaker` breaks with the cue ball in the kitchen. */
export function newGame(seed: number | string, breaker: PlayerIndex = 0): GameState {
  return {
    balls: rackBalls(makeRng(seed)),
    turn: breaker,
    groups: [null, null],
    isBreak: true,
    ballInHand: "kitchen",
    winner: null,
    endReason: null,
    shotNumber: 0,
    stats: [emptyStats(), emptyStats()],
    tray: [],
    lastOutcome: null,
  };
}

export const other = (p: PlayerIndex): PlayerIndex => (p === 0 ? 1 : 0);

export function groupOf(id: number): Group | null {
  if (isSolid(id)) return "solids";
  if (isStripe(id)) return "stripes";
  return null;
}

export const otherGroup = (g: Group): Group => (g === "solids" ? "stripes" : "solids");

export function groupBallIds(g: Group): number[] {
  return g === "solids" ? [1, 2, 3, 4, 5, 6, 7] : [9, 10, 11, 12, 13, 14, 15];
}

/** Balls of `g` still on the table. */
export function ballsLeft(balls: readonly Ball[], g: Group): number[] {
  return groupBallIds(g).filter((id) => balls[id].pocket < 0);
}

/** Has `player` been given a group and pocketed all seven of it? */
export function isGroupCleared(state: GameState, player: PlayerIndex): boolean {
  const g = state.groups[player];
  return g !== null && ballsLeft(state.balls, g).length === 0;
}

/**
 * Balls the shooter may legally strike first: anything but the 8 on an open
 * table, their own group once assigned, and the 8 once that group is gone.
 */
export function legalTargets(state: GameState): number[] {
  const g = state.groups[state.turn];
  if (state.isBreak) return state.balls.filter((b) => b.id !== 0 && b.pocket < 0).map((b) => b.id);
  if (g === null) return state.balls.filter((b) => b.id !== 0 && b.id !== 8 && b.pocket < 0).map((b) => b.id);
  const left = ballsLeft(state.balls, g);
  return left.length > 0 ? left : [8];
}

export function isLegalFirstContact(state: GameState, id: number): boolean {
  return legalTargets(state).includes(id);
}

/** Move the cue ball while it is in hand. Returns null if the spot is illegal. */
export function placeCueBall(state: GameState, x: number, y: number): GameState | null {
  if (state.ballInHand === "none" || state.winner !== null) return null;
  if (!isValidPlacement(state.balls, x, y, state.ballInHand)) return null;
  const balls = cloneBalls(state.balls);
  balls[0].x = x;
  balls[0].y = y;
  return { ...state, balls };
}

/**
 * Apply the rules to a finished shot. `events` and `after` come from the
 * simulation; the returned state is ready for the next stroke, with the
 * verdict in `lastOutcome`.
 */
export function resolveShot(state: GameState, events: ShotEvents, after: readonly Ball[]): GameState {
  const shooter = state.turn;
  const opponent = other(shooter);
  const balls = cloneBalls(after);
  for (const b of balls) b.vx = b.vy = b.ux = b.uy = 0;

  const potted = events.pocketed.filter((p) => p.id !== 0).map((p) => p.id);
  const eightDown = potted.includes(8);
  const wasBreak = state.isBreak;

  const fouls: Foul[] = [];
  if (events.scratch) fouls.push("scratch");
  if (events.firstContact === null) {
    fouls.push("no-contact");
  } else {
    if (!wasBreak && !isLegalFirstContact(state, events.firstContact)) fouls.push("wrong-ball");
    if (potted.length === 0 && events.railsAfterContact === 0) fouls.push("no-rail");
  }
  const foul = fouls.length > 0;

  const stats: [PlayerStats, PlayerStats] = [{ ...state.stats[0] }, { ...state.stats[1] }];
  stats[shooter].shots += 1;
  stats[shooter].potted += potted.filter((id) => id !== 8 || !wasBreak).length;
  if (foul) stats[shooter].fouls += 1;

  const base = {
    shooter,
    wasBreak,
    fouls,
    potted,
    assigned: null as Group | null,
    keepsTurn: false,
    respottedEight: false,
    winner: null as PlayerIndex | null,
    endReason: null as EndReason | null,
  };

  // The 8 decides the game — except on the break, where it is spotted again.
  if (eightDown && !wasBreak) {
    const cleared = isGroupCleared(state, shooter);
    const won = cleared && !foul;
    const outcome: ShotOutcome = {
      ...base,
      winner: won ? shooter : opponent,
      endReason: won ? "eight-legal" : foul ? "eight-foul" : "eight-early",
    };
    return {
      ...state,
      balls,
      stats,
      tray: [...state.tray, ...potted],
      winner: outcome.winner,
      endReason: outcome.endReason,
      ballInHand: "none",
      isBreak: false,
      shotNumber: state.shotNumber + 1,
      lastOutcome: outcome,
    };
  }

  let respottedEight = false;
  if (eightDown) {
    spotBall(balls, 8);
    respottedEight = true;
  }
  const trayAdds = potted.filter((id) => !(id === 8 && respottedEight));

  let groups = state.groups;
  let assigned: Group | null = null;
  let keepsTurn = false;
  if (!foul) {
    if (wasBreak) {
      // Any ball down on a legal break keeps the table; it stays open.
      keepsTurn = potted.length > 0;
    } else if (groups[shooter] === null) {
      const first = potted.find((id) => id !== 8);
      if (first !== undefined) {
        assigned = groupOf(first);
        if (assigned) {
          groups = shooter === 0 ? [assigned, otherGroup(assigned)] : [otherGroup(assigned), assigned];
          keepsTurn = true;
        }
      }
    } else {
      const mine = groups[shooter];
      keepsTurn = potted.some((id) => groupOf(id) === mine);
    }
  }

  let ballInHand: BallInHand = "none";
  if (foul) {
    ballInHand = wasBreak && events.scratch ? "kitchen" : "anywhere";
    if (events.scratch) {
      // Bring the cue ball back to the table for the incoming player to move.
      const home = ballInHand === "kitchen" ? BREAK_SPOT : HEAD_SPOT;
      const spot = nearestFreeSpot(balls, home.x, home.y, ballInHand);
      balls[0].x = spot.x;
      balls[0].y = spot.y;
      balls[0].pocket = -1;
    }
  }

  const outcome: ShotOutcome = { ...base, assigned, keepsTurn, respottedEight };
  return {
    ...state,
    balls,
    groups,
    stats,
    tray: [...state.tray, ...trayAdds],
    turn: keepsTurn ? shooter : opponent,
    isBreak: false,
    ballInHand,
    shotNumber: state.shotNumber + 1,
    lastOutcome: outcome,
  };
}

const FOUL_TEXT: Record<Foul, string> = {
  scratch: "Scratch",
  "no-contact": "No ball hit",
  "wrong-ball": "Wrong ball first",
  "no-rail": "No rail after contact",
};

/** Short human-readable reason for the most important foul of a shot. */
export function foulText(fouls: readonly Foul[]): string {
  if (fouls.length === 0) return "";
  const order: Foul[] = ["scratch", "no-contact", "wrong-ball", "no-rail"];
  const main = order.find((f) => fouls.includes(f)) ?? fouls[0];
  return FOUL_TEXT[main];
}
