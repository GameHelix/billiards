import { describe, expect, it } from "vitest";
import { FOOT_SPOT, HEAD_STRING_X } from "@/lib/game/constants";
import {
  foulText,
  isGroupCleared,
  legalTargets,
  newGame,
  placeCueBall,
  resolveShot,
  type GameState,
} from "@/lib/game/rules";
import { isValidPlacement } from "@/lib/game/table";
import type { Group } from "@/lib/game/types";
import { events, pocketed, stateWith } from "./helpers";

/** Mid-game: player 0 on solids, both groups well stocked, player 0 to shoot. */
const midGame = (turn: 0 | 1 = 0, groups: [Group | null, Group | null] = ["solids", "stripes"]) =>
  stateWith(
    {
      0: [500, 500],
      1: [900, 300],
      2: [1100, 700],
      3: [1300, 400],
      8: [1500, 500],
      9: [1000, 200],
      10: [1200, 850],
      11: [1600, 300],
    },
    { turn, groups }
  );

/** Player 0 on solids with every solid already down; only the 8 is left to win. */
const onTheEight = (): GameState => stateWith({ 0: [500, 500], 8: [1500, 500], 9: [1000, 200], 12: [700, 800] }, { groups: ["solids", "stripes"] });

/** Play out a shot's verdict: `down` lists the balls pocketed, in order. */
function shoot(state: GameState, firstContact: number | null, down: number[], rails = 1) {
  const ev = events({
    firstContact,
    railsAfterContact: rails,
    pocketed: down.map((id, i) => ({ id, pocket: 0, t: i * 0.1 })),
    scratch: down.includes(0),
  });
  return resolveShot(state, ev, pocketed(state.balls, down));
}

describe("fouls", () => {
  it("scratching hands the opponent the cue ball in hand anywhere, back on the table", () => {
    const next = shoot(midGame(), 1, [0]);
    expect(next.lastOutcome?.fouls).toEqual(["scratch"]);
    expect(next.turn).toBe(1);
    expect(next.ballInHand).toBe("anywhere");
    expect(next.balls[0].pocket).toBe(-1);
    expect(isValidPlacement(next.balls, next.balls[0].x, next.balls[0].y, "anywhere")).toBe(true);
    expect(foulText(next.lastOutcome!.fouls)).toBe("Scratch");
  });

  it("a scratch on the break puts the cue ball behind the head string", () => {
    const g = newGame(3);
    const next = shoot(g, 5, [0, 9]);
    expect(next.ballInHand).toBe("kitchen");
    expect(next.turn).toBe(1);
    expect(next.balls[0].x).toBeLessThanOrEqual(HEAD_STRING_X);
    expect(next.groups).toEqual([null, null]);
  });

  it("missing every ball is a foul", () => {
    const next = shoot(midGame(), null, [], 2);
    expect(next.lastOutcome?.fouls).toEqual(["no-contact"]);
    expect(next.ballInHand).toBe("anywhere");
    expect(next.turn).toBe(1);
  });

  it("hitting the opponent's group first is a foul", () => {
    const next = shoot(midGame(), 9, []);
    expect(next.lastOutcome?.fouls).toContain("wrong-ball");
    expect(next.turn).toBe(1);
  });

  it("hitting the 8 first on an open table is a foul, any other ball is fine", () => {
    const open = midGame(0, [null, null]);
    expect(shoot(open, 8, []).lastOutcome?.fouls).toContain("wrong-ball");
    expect(shoot(open, 9, []).lastOutcome?.fouls).toEqual([]);
    expect(shoot(open, 3, []).lastOutcome?.fouls).toEqual([]);
  });

  it("once a group is cleared, the 8 must be struck first", () => {
    expect(shoot(onTheEight(), 9, []).lastOutcome?.fouls).toContain("wrong-ball");
    expect(shoot(onTheEight(), 8, []).lastOutcome?.fouls).toEqual([]);
  });

  it("no ball pocketed and no cushion after contact is a foul", () => {
    const next = shoot(midGame(), 1, [], 0);
    expect(next.lastOutcome?.fouls).toEqual(["no-rail"]);
    expect(foulText(next.lastOutcome!.fouls)).toBe("No rail after contact");
    // Potting a ball satisfies the rule without any cushion.
    expect(shoot(midGame(), 1, [1], 0).lastOutcome?.fouls).toEqual([]);
  });

  it("leaves balls pocketed on a foul down, and counts the foul", () => {
    const next = shoot(midGame(), 9, [3]);
    expect(next.balls[3].pocket).toBeGreaterThanOrEqual(0);
    expect(next.tray).toEqual([3]);
    expect(next.stats[0]).toEqual({ shots: 1, potted: 1, fouls: 1 });
  });
});

describe("groups and turns", () => {
  it("assigns the group of the first ball legally pocketed after the break", () => {
    const next = shoot(midGame(0, [null, null]), 3, [11, 2]);
    expect(next.groups).toEqual(["stripes", "solids"]);
    expect(next.lastOutcome?.assigned).toBe("stripes");
    expect(next.turn).toBe(0);
  });

  it("assigns to whoever shoots, as player two too", () => {
    const next = shoot(midGame(1, [null, null]), 2, [2]);
    expect(next.groups).toEqual(["stripes", "solids"]);
    expect(next.turn).toBe(1);
  });

  it("leaves the table open after balls drop on the break, and the breaker continues", () => {
    const g = newGame(11);
    const next = shoot(g, 4, [4, 13]);
    expect(next.groups).toEqual([null, null]);
    expect(next.isBreak).toBe(false);
    expect(next.turn).toBe(0);
    expect(next.ballInHand).toBe("none");
  });

  it("does not assign a group on a foul", () => {
    const next = shoot(midGame(0, [null, null]), 3, [3, 0]);
    expect(next.groups).toEqual([null, null]);
    expect(next.turn).toBe(1);
  });

  it("keeps the shooter at the table only after potting one of their own", () => {
    expect(shoot(midGame(), 1, [1]).turn).toBe(0);
    expect(shoot(midGame(), 1, [9]).turn).toBe(1);
    expect(shoot(midGame(), 1, [9, 2]).turn).toBe(0);
    expect(shoot(midGame(), 1, []).turn).toBe(1);
    expect(shoot(midGame(), 1, []).ballInHand).toBe("none");
  });

  it("names the legal object balls for the shooter", () => {
    expect(legalTargets(midGame(0, [null, null])).sort((a, b) => a - b)).toEqual([1, 2, 3, 9, 10, 11]);
    expect(legalTargets(midGame())).toEqual([1, 2, 3]);
    expect(legalTargets(midGame(1))).toEqual([9, 10, 11]);
    expect(legalTargets(onTheEight())).toEqual([8]);
    expect(isGroupCleared(onTheEight(), 0)).toBe(true);
    expect(legalTargets(newGame(1))).toHaveLength(15);
  });
});

describe("the 8-ball", () => {
  it("wins when pocketed legally after the group is cleared", () => {
    const next = shoot(onTheEight(), 8, [8]);
    expect(next.winner).toBe(0);
    expect(next.endReason).toBe("eight-legal");
  });

  it("loses when pocketed before the group is cleared", () => {
    const next = shoot(midGame(), 1, [8]);
    expect(next.winner).toBe(1);
    expect(next.endReason).toBe("eight-early");
  });

  it("loses when pocketed on an open table", () => {
    expect(shoot(midGame(0, [null, null]), 3, [3, 8]).winner).toBe(1);
  });

  it("loses when the last ball of the group and the 8 drop on the same shot", () => {
    const g = stateWith({ 0: [500, 500], 7: [900, 500], 8: [1500, 500], 9: [1000, 200] }, { groups: ["solids", "stripes"] });
    const next = shoot(g, 7, [7, 8]);
    expect(next.winner).toBe(1);
    expect(next.endReason).toBe("eight-early");
  });

  it("loses when pocketed together with a foul, even with the group cleared", () => {
    const scratch = shoot(onTheEight(), 8, [8, 0]);
    expect(scratch.winner).toBe(1);
    expect(scratch.endReason).toBe("eight-foul");
    const wrongFirst = shoot(onTheEight(), 9, [8]);
    expect(wrongFirst.winner).toBe(1);
    expect(wrongFirst.endReason).toBe("eight-foul");
  });

  it("is spotted again when it drops on the break, and the breaker continues", () => {
    // The rack has scattered, so the foot spot is free again.
    const g = stateWith({ 0: [900, 500], 1: [1200, 300], 8: [1900, 900], 9: [1700, 700] }, { isBreak: true });
    const next = shoot(g, 1, [8]);
    expect(next.winner).toBeNull();
    expect(next.lastOutcome?.respottedEight).toBe(true);
    expect([next.balls[8].x, next.balls[8].y, next.balls[8].pocket]).toEqual([FOOT_SPOT.x, FOOT_SPOT.y, -1]);
    expect(next.turn).toBe(0);
    expect(next.tray).toEqual([]);
  });

  it("is spotted on a break scratch too, with the cue ball in hand in the kitchen", () => {
    const g = newGame(5);
    const next = shoot(g, 1, [8, 0]);
    expect(next.winner).toBeNull();
    expect(next.balls[8].pocket).toBe(-1);
    expect(next.turn).toBe(1);
    expect(next.ballInHand).toBe("kitchen");
  });
});

describe("ball in hand", () => {
  it("only moves the cue ball while it is in hand, and only to a legal spot", () => {
    const g = newGame(2);
    expect(g.ballInHand).toBe("kitchen");
    expect(placeCueBall(g, 300, 700)?.balls[0]).toMatchObject({ x: 300, y: 700 });
    expect(placeCueBall(g, 1200, 500)).toBeNull();
    expect(placeCueBall(midGame(), 300, 700)).toBeNull();
  });
});
