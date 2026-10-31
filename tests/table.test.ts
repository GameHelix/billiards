import { describe, expect, it } from "vitest";
import { BALL_D, BALL_R, FOOT_SPOT, HEAD_STRING_X, TABLE_L, TABLE_W } from "@/lib/game/constants";
import { makeRng } from "@/lib/game/rng";
import {
  isSolid,
  isStripe,
  isValidPlacement,
  nearestFreeSpot,
  rackBalls,
  rackSlots,
  spotBall,
  TABLE,
} from "@/lib/game/table";
import { layout } from "./helpers";

describe("the rack", () => {
  it("indexes sixteen balls by number with the cue ball behind the head string", () => {
    const balls = rackBalls(makeRng(1));
    expect(balls).toHaveLength(16);
    balls.forEach((b, i) => {
      expect(b.id).toBe(i);
      expect(b.pocket).toBe(-1);
    });
    expect(balls[0].x).toBeLessThanOrEqual(HEAD_STRING_X);
  });

  it("puts the 8 in the middle of the third row and a solid and a stripe in the back corners", () => {
    const slots = rackSlots();
    for (let seed = 0; seed < 25; seed++) {
      const balls = rackBalls(makeRng(seed));
      const at = (slot: number) => balls.find((b) => b.id !== 0 && b.x === slots[slot].x && b.y === slots[slot].y)!.id;
      expect(at(4)).toBe(8);
      const corners = [at(10), at(14)];
      expect(corners.filter(isSolid)).toHaveLength(1);
      expect(corners.filter(isStripe)).toHaveLength(1);
    }
  });

  it("racks tight: neighbours touch, nothing overlaps, the apex sits on the foot spot", () => {
    const balls = rackBalls(makeRng(7)).slice(1);
    expect(balls.some((b) => b.x === FOOT_SPOT.x && b.y === FOOT_SPOT.y)).toBe(true);
    let touching = 0;
    for (let i = 0; i < balls.length; i++) {
      for (let j = i + 1; j < balls.length; j++) {
        const d = Math.hypot(balls[i].x - balls[j].x, balls[i].y - balls[j].y);
        expect(d).toBeGreaterThan(BALL_D - 1e-6);
        if (d < BALL_D + 1e-6) touching += 1;
      }
    }
    // A five-row triangle has 30 internal contacts.
    expect(touching).toBe(30);
  });

  it("is reproducible from its seed and shuffled between seeds", () => {
    expect(rackBalls(makeRng("same"))).toEqual(rackBalls(makeRng("same")));
    const orders = new Set<string>();
    for (let seed = 0; seed < 12; seed++) {
      orders.add(rackSlots().map((s) => rackBalls(makeRng(seed)).find((b) => b.x === s.x && b.y === s.y)!.id).join(","));
    }
    expect(orders.size).toBeGreaterThan(10);
  });
});

describe("table geometry", () => {
  it("has four corner and two side pockets around the cloth", () => {
    expect(TABLE.pockets.map((p) => p.kind)).toEqual(["corner", "side", "corner", "corner", "side", "corner"]);
    for (const p of TABLE.pockets) {
      // Every capture centre lies just off the cloth, behind its mouth.
      const outside = p.x <= 0 || p.x >= TABLE_L || p.y <= 0 || p.y >= TABLE_W;
      expect(outside).toBe(true);
    }
  });

  it("builds cushions whose playable side faces the cloth", () => {
    const mains = TABLE.segments.filter((s) => s.len > 300);
    expect(mains).toHaveLength(6);
    for (const s of TABLE.segments) {
      expect(Math.hypot(s.nx, s.ny)).toBeCloseTo(1, 9);
      expect(Math.hypot(s.dx, s.dy)).toBeCloseTo(1, 9);
    }
    for (const s of mains) {
      const mx = (s.ax + s.bx) / 2;
      const my = (s.ay + s.by) / 2;
      expect((TABLE_L / 2 - mx) * s.nx + (TABLE_W / 2 - my) * s.ny).toBeGreaterThan(0);
    }
  });

  it("is symmetric end to end", () => {
    const key = (x: number, y: number) => `${Math.round(x * 1000)}:${Math.round(y * 1000)}`;
    const points = new Set(TABLE.circles.map((c) => key(c.x, c.y)));
    for (const c of TABLE.circles) expect(points.has(key(TABLE_L - c.x, c.y))).toBe(true);
  });
});

describe("cue-ball placement", () => {
  const balls = layout({ 0: [300, 300], 1: [400, 500], 2: [1500, 500] });

  it("accepts free spots and rejects overlaps and spots off the cloth", () => {
    expect(isValidPlacement(balls, 250, 700, "anywhere")).toBe(true);
    expect(isValidPlacement(balls, 400 + BALL_D - 1, 500, "anywhere")).toBe(false);
    expect(isValidPlacement(balls, BALL_R - 1, 500, "anywhere")).toBe(false);
    expect(isValidPlacement(balls, 900, TABLE_W - BALL_R + 1, "anywhere")).toBe(false);
  });

  it("keeps the cue ball behind the head string after a break scratch", () => {
    expect(isValidPlacement(balls, 1200, 500, "anywhere")).toBe(true);
    expect(isValidPlacement(balls, 1200, 500, "kitchen")).toBe(false);
    expect(isValidPlacement(balls, HEAD_STRING_X - 1, 200, "kitchen")).toBe(true);
  });

  it("finds the nearest free spot when the wanted one is taken", () => {
    const spot = nearestFreeSpot(balls, 400, 500, "kitchen");
    expect(isValidPlacement(balls, spot.x, spot.y, "kitchen")).toBe(true);
    expect(Math.hypot(spot.x - 400, spot.y - 500)).toBeLessThan(BALL_D + 10);
  });

  it("re-spots a ball on the foot spot, or right behind whatever sits there", () => {
    const free = layout({ 0: [300, 300] });
    spotBall(free, 8);
    expect([free[8].x, free[8].y, free[8].pocket]).toEqual([FOOT_SPOT.x, FOOT_SPOT.y, -1]);

    const blocked = layout({ 0: [300, 300], 5: [FOOT_SPOT.x, FOOT_SPOT.y] });
    spotBall(blocked, 8);
    expect(blocked[8].y).toBe(FOOT_SPOT.y);
    expect(blocked[8].x).toBeGreaterThan(FOOT_SPOT.x);
    expect(blocked[8].x - FOOT_SPOT.x).toBeGreaterThanOrEqual(BALL_D);
    expect(blocked[8].x - FOOT_SPOT.x).toBeLessThan(BALL_D + 2);
  });
});
