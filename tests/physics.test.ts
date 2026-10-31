import { describe, expect, it } from "vitest";
import {
  BALL_D,
  BALL_R,
  BALL_RESTITUTION,
  CUSHION_GRIP,
  CUSHION_RESTITUTION,
  MAX_SPEED,
  MAX_TIP_OFFSET,
  TABLE_L,
  TABLE_W,
  TICK,
} from "@/lib/game/constants";
import { createSim, simulateShot, step, strike, travelDistance, type Sim } from "@/lib/game/physics";
import { makeRng } from "@/lib/game/rng";
import { rackBalls, TABLE } from "@/lib/game/table";
import type { Ball } from "@/lib/game/types";
import { layout, toDeg, toRad } from "./helpers";

const powerFor = (speed: number) => speed / MAX_SPEED;
/** Spin setting that starts the cue ball in natural roll (tip at 2/5 R). */
const NATURAL_ROLL = 0.8;

function momentum(balls: readonly Ball[]) {
  let x = 0;
  let y = 0;
  for (const b of balls) {
    if (b.pocket >= 0) continue;
    x += b.vx;
    y += b.vy;
  }
  return { x, y };
}

/** Tick until the first ball-ball contact; returns velocities either side of it. */
function untilFirstContact(sim: Sim) {
  let before = sim.balls.map((b) => ({ ...b }));
  for (let i = 0; i < 2000 && sim.events.firstContact === null; i++) {
    before = sim.balls.map((b) => ({ ...b }));
    step(sim);
  }
  return { before, after: sim.balls };
}

describe("ball-ball collisions", () => {
  it("passes a head-on hit almost entirely to the object ball", () => {
    const sim = createSim(layout({ 0: [500, 500], 5: [900, 500] }));
    strike(sim, { angle: 0, power: powerFor(3000), spin: 0 });
    const { before, after } = untilFirstContact(sim);
    expect(sim.events.firstContact).toBe(5);
    const vIn = before[0].vx;
    expect(after[5].vx / vIn).toBeGreaterThan(0.93);
    expect(after[5].vx / vIn).toBeLessThan((1 + BALL_RESTITUTION) / 2 + 1e-9);
    expect(Math.abs(after[0].vx) / vIn).toBeLessThan(0.06);
    expect(Math.abs(after[5].vy)).toBeLessThan(1e-6);
  });

  it("splits a half-ball cut at (nearly) 90 degrees along the line of centres", () => {
    // Impact parameter of one radius: a 30° cut.
    const sim = createSim(layout({ 0: [400, 500], 6: [1000, 500 + BALL_R] }));
    strike(sim, { angle: 0, power: powerFor(2500), spin: 0 });
    const { after } = untilFirstContact(sim);
    const cue = after[0];
    const obj = after[6];
    const objAngle = toDeg(Math.atan2(obj.vy, obj.vx));
    expect(objAngle).toBeCloseTo(30, 0);
    const split = toDeg(Math.acos((cue.vx * obj.vx + cue.vy * obj.vy) / (Math.hypot(cue.vx, cue.vy) * Math.hypot(obj.vx, obj.vy))));
    // Exactly 90° for perfectly elastic balls; restitution keeps a sliver of
    // the normal speed with the cue ball.
    expect(split).toBeGreaterThan(85);
    expect(split).toBeLessThanOrEqual(90.01);
  });

  it("conserves momentum through a collision, within friction's share of a tick", () => {
    for (const offset of [0, 0.4, 0.8, 1.4]) {
      const sim = createSim(layout({ 0: [300, 400], 9: [1100, 400 + offset * BALL_R] }));
      strike(sim, { angle: 0, power: powerFor(4000), spin: NATURAL_ROLL });
      const { before, after } = untilFirstContact(sim);
      const p0 = momentum(before);
      const p1 = momentum(after);
      const tolerance = 0.02 * Math.hypot(p0.x, p0.y);
      expect(Math.abs(p1.x - p0.x)).toBeLessThan(tolerance);
      expect(Math.abs(p1.y - p0.y)).toBeLessThan(tolerance);
    }
  });

  it("conserves momentum when the cue ball hits a group of frozen balls", () => {
    const y = 500;
    const sim = createSim(layout({ 0: [400, y], 1: [900, y], 2: [900 + BALL_D, y], 3: [900 + BALL_D * 1.5, y + BALL_D * Math.sin(Math.PI / 3)] }));
    strike(sim, { angle: 0, power: powerFor(3500), spin: NATURAL_ROLL });
    const { before, after } = untilFirstContact(sim);
    const p0 = momentum(before);
    const p1 = momentum(after);
    expect(Math.abs(p1.x - p0.x)).toBeLessThan(0.02 * p0.x);
    expect(Math.abs(p1.y - p0.y)).toBeLessThan(0.02 * p0.x);
    // The impulse reaches the far balls rather than stopping at the first.
    expect(Math.hypot(after[2].vx, after[2].vy) + Math.hypot(after[3].vx, after[3].vy)).toBeGreaterThan(500);
  });

  it("never lets a full-power ball tunnel past a ball it only grazes", () => {
    const sim = createSim(layout({ 0: [200, 500], 4: [1500, 500 + BALL_D - 0.4] }));
    strike(sim, { angle: 0, power: 1, spin: 0 });
    // A full-power tick covers ~52 mm, far more than the 0.4 mm overlap.
    expect((MAX_SPEED * TICK) / 0.4).toBeGreaterThan(100);
    while (!sim.settled) step(sim);
    expect(sim.events.firstContact).toBe(4);
  });

  it("keeps every ball apart and on the cloth through full-power shots", () => {
    const rng = makeRng("tunnel");
    let worstOverlap = 0;
    let escapes = 0;
    let unsettled = 0;
    let fastest = 0;
    for (let trial = 0; trial < 24; trial++) {
      const balls = rackBalls(makeRng(trial));
      balls[0].x = 100 + rng.next() * 380;
      balls[0].y = 60 + rng.next() * (TABLE_W - 120);
      const sim = createSim(balls, false);
      strike(sim, { angle: rng.next() * Math.PI * 2, power: 1, spin: rng.next() * 2 - 1 });
      for (let tick = 0; tick < 3000 && !sim.settled; tick++) {
        step(sim);
        const on = sim.balls.filter((b) => b.pocket < 0);
        for (const b of on) {
          fastest = Math.max(fastest, Math.hypot(b.vx, b.vy));
          const inside =
            b.x >= BALL_R - 0.01 && b.x <= TABLE_L - BALL_R + 0.01 && b.y >= BALL_R - 0.01 && b.y <= TABLE_W - BALL_R + 0.01;
          // Only a pocket mouth lets a centre past a cushion line.
          if (!inside && !TABLE.pockets.some((p) => Math.hypot(b.x - p.mouthX, b.y - p.mouthY) < BALL_R * 3)) {
            escapes += 1;
          }
        }
        for (let i = 0; i < on.length; i++) {
          for (let j = i + 1; j < on.length; j++) {
            const gap = Math.hypot(on[i].x - on[j].x, on[i].y - on[j].y) - BALL_D;
            if (-gap > worstOverlap) worstOverlap = -gap;
          }
        }
      }
      if (!sim.settled) unsettled += 1;
    }
    expect(escapes).toBe(0);
    expect(worstOverlap).toBeLessThan(0.01);
    expect(unsettled).toBe(0);
    // Collisions only ever lose energy, so nothing outruns the cue ball's
    // best: full power with full follow, whose spin pays out (5v + 2u)/7.
    expect(fastest).toBeLessThanOrEqual((MAX_SPEED * (5 + 2 * 2.5 * MAX_TIP_OFFSET)) / 7 + 1e-6);
  });
});

describe("cushions", () => {
  it("reflects off a cushion: normal speed scaled by restitution, angle mirrored", () => {
    // Heads for the top cushion about 700 mm from the head end, well clear of
    // any pocket.
    const sim = createSim(layout({ 0: [300, 500] }));
    strike(sim, { angle: toRad(-50), power: powerFor(2500), spin: NATURAL_ROLL });
    let before = { ...sim.balls[0] };
    for (let i = 0; i < 1000 && !sim.impacts.some((imp) => imp.kind === "cushion"); i++) {
      before = { ...sim.balls[0] };
      step(sim);
    }
    expect(sim.impacts.some((imp) => imp.kind === "cushion")).toBe(true);
    const after = sim.balls[0];
    expect(before.vy).toBeLessThan(0);
    expect(after.vy).toBeGreaterThan(0);
    expect(after.vy / -before.vy).toBeCloseTo(CUSHION_RESTITUTION, 1);
    expect(after.vx / before.vx).toBeCloseTo(CUSHION_GRIP, 1);
    // It came off the top cushion, nowhere near a pocket.
    expect(after.y).toBeGreaterThan(BALL_R - 0.01);
  });

  it("rattles a ball off the rounded jaw of a side pocket when it comes in too steep", () => {
    // 70° off the pocket's axis, aimed at the middle of the mouth.
    const a = toRad(70);
    const x = TABLE_L / 2 - 400 * Math.sin(a);
    const y = 400 * Math.cos(a);
    const res = simulateShot(layout({ 0: [x, y] }), {
      angle: Math.atan2(-y, TABLE_L / 2 - x),
      power: powerFor(1500),
      spin: NATURAL_ROLL,
    });
    expect(res.events.pocketed).toHaveLength(0);
  });
});

describe("pockets", () => {
  it("captures a ball sent into each of the six pockets, and records which", () => {
    TABLE.pockets.forEach((p, index) => {
      const sx = p.mouthX - p.outX * 350;
      const sy = p.mouthY - p.outY * 350;
      const res = simulateShot(layout({ 0: [sx, sy] }), {
        angle: Math.atan2(p.y - sy, p.x - sx),
        power: powerFor(1500),
        spin: NATURAL_ROLL,
      });
      expect(res.events.pocketed).toEqual([expect.objectContaining({ id: 0, pocket: index })]);
      expect(res.events.scratch).toBe(true);
      expect(res.balls[0].pocket).toBe(index);
    });
  });

  it("lets a ball frozen to the rail roll past a side pocket but into a corner", () => {
    const past = simulateShot(layout({ 0: [1400, BALL_R] }), { angle: Math.PI, power: powerFor(700), spin: NATURAL_ROLL });
    expect(past.events.pocketed).toHaveLength(0);
    expect(past.balls[0].x).toBeLessThan(TABLE_L / 2);
    const corner = simulateShot(layout({ 0: [400, BALL_R] }), { angle: Math.PI, power: powerFor(900), spin: NATURAL_ROLL });
    expect(corner.events.pocketed.map((p) => p.pocket)).toEqual([0]);
  });

  it("does not capture a ball that stops short of the pocket", () => {
    const p = TABLE.pockets[2];
    const sx = p.mouthX - p.outX * 500;
    const sy = p.mouthY - p.outY * 500;
    const res = simulateShot(layout({ 0: [sx, sy] }), {
      angle: Math.atan2(p.y - sy, p.x - sx),
      power: powerFor(300),
      spin: NATURAL_ROLL,
    });
    expect(res.events.pocketed).toHaveLength(0);
    expect(res.balls[0].pocket).toBe(-1);
  });
});

describe("friction", () => {
  it("brings a full-power break to a dead stop within twelve seconds", () => {
    const sim = createSim(rackBalls(makeRng(42)));
    strike(sim, { angle: Math.atan2(TABLE_W / 2 - sim.balls[0].y, TABLE_L * 0.75 - sim.balls[0].x), power: 1, spin: 0 });
    let ticks = 0;
    while (!sim.settled && ticks < 2000) {
      step(sim);
      ticks += 1;
    }
    expect(sim.settled).toBe(true);
    expect(ticks * TICK).toBeLessThan(12);
    for (const b of sim.balls) {
      expect([b.vx, b.vy, b.ux, b.uy]).toEqual([0, 0, 0, 0]);
    }
  });

  it("slows a ball smoothly: a gentle roll stops quickly and nearby", () => {
    const res = simulateShot(layout({ 0: [500, 500] }), { angle: 0, power: powerFor(200), spin: NATURAL_ROLL });
    expect(res.time).toBeLessThan(2);
    expect(res.balls[0].x - 500).toBeGreaterThan(20);
    expect(res.balls[0].x - 500).toBeLessThan(200);
  });

  it("covers more ground the harder it is hit", () => {
    const d = [500, 1000, 2000, 4000].map((v) => travelDistance(v, 0));
    for (let i = 1; i < d.length; i++) expect(d[i]).toBeGreaterThan(d[i - 1]);
  });

  it("follows through with topspin, draws back with backspin and stops dead on a stun", () => {
    /** Cue-ball x a little after it strikes ball 3 full in the face. */
    const cueAfterContact = (spin: number, from: number) => {
      const sim = createSim(layout({ 0: [from, 500], 3: [1000, 500] }), false);
      strike(sim, { angle: 0, power: powerFor(2200), spin });
      untilFirstContact(sim);
      const contactX = sim.balls[0].x;
      for (let i = 0; i < 90; i++) step(sim); // three quarters of a second
      return sim.balls[0].x - contactX;
    };
    expect(cueAfterContact(1, 400)).toBeGreaterThan(150);
    expect(cueAfterContact(-1, 700)).toBeLessThan(-150);
    // Close in, a centre-ball hit arrives sliding and stops on the spot.
    expect(Math.abs(cueAfterContact(0, 880))).toBeLessThan(15);
  });
});

describe("event log", () => {
  it("records the first contact, cushions after it, pots in order and a scratch", () => {
    // Ball 3 sits on the line to the top-right corner; the cue ball follows
    // it in.
    const p = TABLE.pockets[2];
    const ox = p.mouthX - p.outX * 300;
    const oy = p.mouthY - p.outY * 300;
    const cx = ox - p.outX * 250;
    const cy = oy - p.outY * 250;
    const res = simulateShot(layout({ 0: [cx, cy], 3: [ox, oy], 12: [300, 700] }), {
      angle: Math.atan2(oy - cy, ox - cx),
      power: powerFor(2600),
      spin: 1,
    });
    expect(res.events.firstContact).toBe(3);
    expect(res.events.pocketed.map((e) => e.id)).toEqual([3, 0]);
    expect(res.events.pocketed[0].t).toBeLessThan(res.events.pocketed[1].t);
    expect(res.events.scratch).toBe(true);
  });

  it("counts cushion contacts after the first contact and lists object balls that reached one", () => {
    const res = simulateShot(layout({ 0: [500, 500], 7: [1200, 500] }), { angle: 0, power: powerFor(3000), spin: 0 });
    expect(res.events.firstContact).toBe(7);
    expect(res.events.railsAfterContact).toBeGreaterThan(0);
    expect(res.events.objectBallsToRail).toContain(7);
    expect(res.events.scratch).toBe(false);
  });

  it("reports no contact when the cue ball touches nothing", () => {
    const res = simulateShot(layout({ 0: [500, 500], 7: [1200, 900] }), { angle: Math.PI / 2, power: powerFor(800), spin: 0 });
    expect(res.events.firstContact).toBeNull();
    expect(res.events.railsAfterContact).toBe(0);
  });

  it("is deterministic: the same shot gives the same table", () => {
    const shot = { angle: 0.013, power: 1, spin: 0.2 };
    const a = simulateShot(rackBalls(makeRng(9)), shot);
    const b = simulateShot(rackBalls(makeRng(9)), shot);
    expect(a.balls).toEqual(b.balls);
    expect(a.events).toEqual(b.events);
  });
});

describe("the break", () => {
  it("spreads a tight rack: most balls move and several reach a cushion", () => {
    const balls = rackBalls(makeRng(5));
    const res = simulateShot(balls, {
      angle: Math.atan2(TABLE_W / 2 + 3 - balls[0].y, TABLE_L * 0.75 - balls[0].x),
      power: 1,
      spin: 0,
    });
    const moved = res.balls.filter((b) => b.id !== 0 && Math.hypot(b.x - balls[b.id].x, b.y - balls[b.id].y) > 150);
    expect(moved.length).toBeGreaterThanOrEqual(10);
    expect(res.events.objectBallsToRail.length).toBeGreaterThanOrEqual(5);
  });
});
