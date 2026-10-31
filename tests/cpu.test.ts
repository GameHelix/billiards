import { describe, expect, it } from "vitest";
import { HEAD_STRING_X, MIN_POWER, TABLE_L, TABLE_W } from "@/lib/game/constants";
import { chooseShot, planShot, potWindow, type CpuPlan } from "@/lib/game/cpu";
import { simulateShot } from "@/lib/game/physics";
import { makeRng } from "@/lib/game/rng";
import { newGame, placeCueBall, resolveShot, type GameState } from "@/lib/game/rules";
import { isValidPlacement, TABLE } from "@/lib/game/table";
import type { BallInHand, Difficulty } from "@/lib/game/types";
import { stateWith } from "./helpers";

const TIERS: Difficulty[] = ["easy", "medium", "hard"];

/** Carry out a plan the way the game does: place, strike, apply the rules. */
function execute(state: GameState, plan: CpuPlan): GameState {
  let s = state;
  if (plan.place) {
    const placed = placeCueBall(s, plan.place.x, plan.place.y);
    if (!placed) throw new Error(`illegal placement ${JSON.stringify(plan.place)}`);
    s = placed;
  }
  const res = simulateShot(s.balls, plan.shot);
  return resolveShot(s, res.events, res.balls);
}

function expectWellFormed(state: GameState, plan: CpuPlan) {
  expect(Number.isFinite(plan.shot.angle)).toBe(true);
  expect(plan.shot.power).toBeGreaterThanOrEqual(MIN_POWER);
  expect(plan.shot.power).toBeLessThanOrEqual(1);
  expect(Math.abs(plan.shot.spin)).toBeLessThanOrEqual(1);
  if (state.ballInHand === "none") {
    expect(plan.place).toBeNull();
  } else {
    expect(plan.place).not.toBeNull();
    expect(isValidPlacement(state.balls, plan.place!.x, plan.place!.y, state.ballInHand)).toBe(true);
  }
}

/** A position after a real break, varied by seed. */
function afterBreak(seed: number, ballInHand: BallInHand, groups: GameState["groups"]): GameState {
  const g = newGame(seed);
  const cue = g.balls[0];
  const res = simulateShot(g.balls, {
    angle: Math.atan2(TABLE_W / 2 + ((seed % 7) - 3) * 2 - cue.y, TABLE_L * 0.75 - cue.x),
    power: 1,
    spin: 0,
  });
  const balls = res.balls.map((b) => ({ ...b }));
  if (balls[0].pocket >= 0) {
    balls[0].pocket = -1;
    balls[0].x = 300;
    balls[0].y = 500;
  }
  if (balls[8].pocket >= 0) {
    balls[8].pocket = -1;
    balls[8].x = 1500;
    balls[8].y = 500;
  }
  return { ...g, balls, isBreak: false, ballInHand, groups, turn: 1 };
}

describe("legality", () => {
  it("always returns a well-formed stroke and a legal cue-ball placement", () => {
    const hands: BallInHand[] = ["none", "anywhere", "kitchen"];
    for (const d of TIERS) {
      for (let seed = 1; seed <= 4; seed++) {
        for (const hand of hands) {
          const state = afterBreak(seed, hand, seed % 2 ? ["solids", "stripes"] : [null, null]);
          expectWellFormed(state, chooseShot(state, d, makeRng(seed * 31)));
        }
      }
    }
  });

  it("breaks from behind the head string, hard, at the rack", () => {
    for (const d of TIERS) {
      const g = newGame(4);
      const plan = chooseShot(g, d, makeRng(9));
      expect(plan.kind).toBe("break");
      expect(plan.place!.x).toBeLessThanOrEqual(HEAD_STRING_X);
      expect(plan.shot.power).toBeGreaterThan(0.7);
      const next = execute(g, plan);
      expect(next.lastOutcome?.fouls).not.toContain("no-contact");
    }
  });

  it("plays a whole game against itself to a finish without an illegal placement", () => {
    let s = newGame("selfplay");
    for (let shot = 0; shot < 200 && s.winner === null; shot++) {
      const plan = chooseShot(s, "medium", makeRng(shot));
      expectWellFormed(s, plan);
      s = execute(s, plan);
    }
    expect(s.winner).not.toBeNull();
  });
});

describe("shot making", () => {
  // Ball 1 sits on the long diagonal to the head-end corner, cue ball behind it.
  const straightIn = () =>
    stateWith({ 0: [620, 620], 1: [300, 300], 8: [1500, 700], 9: [1200, 200] }, { groups: ["solids", "stripes"] });

  it("finds a clean window into a pocket for a ball in front of it", () => {
    const w = potWindow(300, 300, 0);
    expect(w).not.toBeNull();
    expect(w!.aim).toBeCloseTo(-3 * (Math.PI / 4), 1);
    expect(w!.half).toBeGreaterThan(0.02);
    // ...and none into a side pocket from far along the rail beside it.
    expect(potWindow(150, 120, 1)).toBeNull();
  });

  it("Hard pots a straight-in shot reliably, without scratching", () => {
    for (let seed = 1; seed <= 10; seed++) {
      const plan = chooseShot(straightIn(), "hard", makeRng(seed));
      expect(plan.kind).toBe("pot");
      expect(plan.target).toBe(1);
      const next = execute(straightIn(), plan);
      expect(next.lastOutcome?.potted).toEqual([1]);
      expect(next.lastOutcome?.fouls).toEqual([]);
      expect(next.turn).toBe(0);
    }
  });

  it("Medium makes it most of the time, Easy far less often", () => {
    const rate = (d: Difficulty) => {
      let made = 0;
      for (let seed = 1; seed <= 20; seed++) {
        if (execute(straightIn(), chooseShot(straightIn(), d, makeRng(seed))).lastOutcome?.potted.includes(1)) made++;
      }
      return made;
    };
    const medium = rate("medium");
    const easy = rate("easy");
    expect(medium).toBeGreaterThanOrEqual(15);
    expect(easy).toBeLessThan(medium);
  });

  it("uses ball in hand to set up a makeable pot", () => {
    const state = stateWith(
      { 0: [300, 500], 1: [700, 200], 2: [1300, 800], 9: [1000, 500], 10: [1500, 600], 8: [1800, 500] },
      { groups: ["solids", "stripes"], ballInHand: "anywhere" }
    );
    let made = 0;
    for (let seed = 1; seed <= 6; seed++) {
      const plan = chooseShot(state, "hard", makeRng(seed));
      expectWellFormed(state, plan);
      const next = execute(state, plan);
      if (next.lastOutcome?.fouls.length === 0 && next.turn === 0) made++;
    }
    expect(made).toBeGreaterThanOrEqual(5);
  });

  it("keeps the cue ball in the kitchen after a break scratch", () => {
    const state = stateWith(
      { 0: [300, 500], 3: [400, 250], 12: [1300, 800], 5: [1600, 400], 8: [1100, 520] },
      { ballInHand: "kitchen" }
    );
    for (const d of TIERS) {
      const plan = chooseShot(state, d, makeRng(3));
      expect(plan.place!.x).toBeLessThanOrEqual(HEAD_STRING_X);
    }
  });

  it("goes for the 8 once its group is cleared, and wins with it", () => {
    const state = stateWith({ 0: [900, 400], 8: [1500, 250], 1: [600, 700], 2: [1200, 850] }, { turn: 1, groups: ["solids", "stripes"] });
    for (let seed = 1; seed <= 6; seed++) {
      const plan = chooseShot(state, "hard", makeRng(seed));
      expect(plan.target).toBe(8);
      expect(execute(state, plan).winner).toBe(1);
    }
  });

  it("plays a legal safety or kick when every pot is blocked", () => {
    // The cue ball is boxed in by stripes; the solids are far away.
    const snookered = stateWith(
      { 0: [200, 500], 9: [320, 500], 10: [290, 430], 11: [290, 570], 1: [1700, 500], 2: [1800, 900], 8: [1000, 100], 12: [1500, 200] },
      { groups: ["solids", "stripes"] }
    );
    let legal = 0;
    for (let seed = 1; seed <= 6; seed++) {
      const plan = chooseShot(snookered, "hard", makeRng(seed));
      expect(plan.kind).toBe("safety");
      if (execute(snookered, plan).lastOutcome?.fouls.length === 0) legal++;
    }
    expect(legal).toBeGreaterThanOrEqual(5);
  });

  it("paces a safety to reach a far ball that has no pot", () => {
    // Stripes to shoot, all of them hugging the far rail, the cue ball in the
    // opposite corner: nothing to pot, a long way to go.
    const far = stateWith(
      { 0: [1919, 964], 1: [287, 935], 3: [1967, 182], 7: [1956, 25], 8: [1699, 500], 12: [395, 929], 14: [193, 934], 15: [453, 946] },
      { groups: ["stripes", "solids"] }
    );
    for (const d of TIERS) {
      let reached = 0;
      let legal = 0;
      for (let seed = 1; seed <= 6; seed++) {
        const next = execute(far, chooseShot(far, d, makeRng(seed)));
        if (!next.lastOutcome?.fouls.includes("no-contact")) reached++;
        if (next.lastOutcome?.fouls.length === 0) legal++;
      }
      expect(reached).toBeGreaterThanOrEqual(5);
      if (d !== "easy") expect(legal).toBeGreaterThanOrEqual(5);
    }
  });

  it("thinks in slices and stays well inside the time budget", () => {
    const state = afterBreak(2, "anywhere", ["solids", "stripes"]);
    const gen = planShot(state, "hard", makeRng(1));
    let slices = 0;
    const t0 = performance.now();
    for (let r = gen.next(); !r.done; r = gen.next()) slices++;
    expect(slices).toBeGreaterThan(3);
    expect(performance.now() - t0).toBeLessThan(1500);
  });
});

describe("pocket windows", () => {
  it("lie inside the pocket mouth from wherever they are measured", () => {
    for (const [i, p] of TABLE.pockets.entries()) {
      const x = p.mouthX - p.outX * 500;
      const y = p.mouthY - p.outY * 500;
      const w = potWindow(x, y, i);
      expect(w).not.toBeNull();
      const toPocket = Math.atan2(p.y - y, p.x - x);
      expect(Math.abs(w!.aim - toPocket)).toBeLessThan(w!.half + 1e-9);
    }
  });
});
