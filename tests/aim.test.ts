import { describe, expect, it } from "vitest";
import { aimGuide, sweep } from "@/lib/game/aim";
import { BALL_D, BALL_R, MAX_SPEED } from "@/lib/game/constants";
import { createSim, step, strike } from "@/lib/game/physics";
import { makeRng } from "@/lib/game/rng";
import { TABLE } from "@/lib/game/table";
import { layout, toDeg } from "./helpers";

describe("sweeping a ball's path", () => {
  it("stops at the first ball in the way, a ball's width short of its centre", () => {
    const balls = layout({ 0: [500, 500], 4: [900, 500], 5: [1300, 500] });
    const hit = sweep(balls, 500, 500, 1, 0, 0, -1, true);
    expect(hit.kind).toBe("ball");
    expect(hit.id).toBe(4);
    expect(hit.dist).toBeCloseTo(400 - BALL_D, 6);
    expect([hit.nx, hit.ny]).toEqual([-1, 0]);
  });

  it("can ignore balls and see through to the cushion", () => {
    const balls = layout({ 0: [500, 500], 4: [900, 500] });
    const hit = sweep(balls, 500, 500, 1, 0, 0, 4, true);
    expect(hit.kind).toBe("cushion");
    expect(hit.dist).toBeCloseTo(2000 - BALL_R - 500, 6);
  });

  it("reports a pocket when the path drops into one first", () => {
    const p = TABLE.pockets[0];
    const balls = layout({ 0: [400, 400] });
    const d = Math.hypot(p.x - 400, p.y - 400);
    const hit = sweep(balls, 400, 400, (p.x - 400) / d, (p.y - 400) / d, 0, -1, true);
    expect(hit.kind).toBe("pocket");
    expect(hit.id).toBe(0);
  });
});

describe("the aiming guide", () => {
  it("shows the ghost ball, the object ball's line and the tangent line for a cut", () => {
    const balls = layout({ 0: [400, 500], 6: [1000, 500 + BALL_R] }); // a 30° cut
    const g = aimGuide(balls, 0);
    expect(g.kind).toBe("ball");
    expect(g.targetId).toBe(6);
    expect(Math.hypot(g.end.x - 1000, g.end.y - (500 + BALL_R))).toBeCloseTo(BALL_D, 6);
    expect(toDeg(Math.atan2(g.objectDir!.y, g.objectDir!.x))).toBeCloseTo(30, 6);
    expect(toDeg(g.cut)).toBeCloseTo(30, 6);
    expect(g.cueDir!.x * g.objectDir!.x + g.cueDir!.y * g.objectDir!.y).toBeCloseTo(0, 9);
  });

  it("mirrors the path off a cushion", () => {
    // Meets the top cushion 770 mm along it, clear of the side pocket.
    const g = aimGuide(layout({ 0: [300, 500] }), -Math.PI / 4);
    expect(g.kind).toBe("cushion");
    expect(g.end.y).toBeCloseTo(BALL_R, 6);
    expect(g.cueDir!.x).toBeCloseTo(Math.SQRT1_2, 6);
    expect(g.cueDir!.y).toBeCloseTo(Math.SQRT1_2, 6);
  });

  it("predicts exactly what the physics does: the ball struck and its line", () => {
    const rng = makeRng("guide");
    const balls = layout({ 0: [600, 480], 1: [1100, 300], 2: [1250, 640], 3: [1500, 450], 9: [900, 820], 10: [1700, 200] });
    let checked = 0;
    for (let i = 0; i < 60; i++) {
      const angle = rng.next() * Math.PI * 2;
      const g = aimGuide(balls, angle);
      if (g.kind !== "ball") continue;
      const sim = createSim(balls, false);
      strike(sim, { angle, power: 3000 / MAX_SPEED, spin: 0 });
      for (let t = 0; t < 600 && sim.events.firstContact === null; t++) step(sim);
      expect(sim.events.firstContact).toBe(g.targetId);
      const o = sim.balls[g.targetId];
      const dir = Math.atan2(o.vy, o.vx);
      const want = Math.atan2(g.objectDir!.y, g.objectDir!.x);
      let diff = Math.abs(dir - want);
      if (diff > Math.PI) diff = Math.PI * 2 - diff;
      expect(toDeg(diff)).toBeLessThan(0.05);
      checked += 1;
    }
    expect(checked).toBeGreaterThan(10);
  });
});
