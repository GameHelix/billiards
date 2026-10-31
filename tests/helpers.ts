import { newGame, type GameState } from "@/lib/game/rules";
import { makeBall } from "@/lib/game/table";
import type { Ball, BallInHand, Group, PlayerIndex, ShotEvents } from "@/lib/game/types";
import { emptyEvents } from "@/lib/game/physics";

/** A pocket index that marks a ball as parked off the table in a test layout. */
export const OFF = 99;

/** Sixteen balls with only the listed ones on the table, at the given spots. */
export function layout(spots: Record<number, readonly [number, number]>): Ball[] {
  const balls: Ball[] = [];
  for (let id = 0; id < 16; id++) {
    const at = spots[id];
    const b = makeBall(id, at ? at[0] : -1000, at ? at[1] : -1000);
    if (!at) b.pocket = OFF;
    balls.push(b);
  }
  return balls;
}

/** A mid-game state on a custom layout. */
export function stateWith(
  spots: Record<number, readonly [number, number]>,
  opts: {
    turn?: PlayerIndex;
    groups?: [Group | null, Group | null];
    ballInHand?: BallInHand;
    isBreak?: boolean;
  } = {}
): GameState {
  const g = newGame(1, opts.turn ?? 0);
  return {
    ...g,
    balls: layout(spots),
    turn: opts.turn ?? 0,
    groups: opts.groups ?? [null, null],
    ballInHand: opts.ballInHand ?? "none",
    isBreak: opts.isBreak ?? false,
  };
}

/** Shot events for rules tests, starting from a clean log. */
export function events(patch: Partial<ShotEvents>): ShotEvents {
  return { ...emptyEvents(), ...patch };
}

/** Mark balls as pocketed in a copy of `balls`, as a finished shot would. */
export function pocketed(balls: readonly Ball[], ids: readonly number[]): Ball[] {
  return balls.map((b) => (ids.includes(b.id) ? { ...b, pocket: 0 } : { ...b }));
}

export const toRad = (deg: number) => (deg * Math.PI) / 180;
export const toDeg = (rad: number) => (rad * 180) / Math.PI;
