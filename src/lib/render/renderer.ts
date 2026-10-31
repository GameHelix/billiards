import { aimGuide, sweep } from "../game/aim";
import {
  BALL_COLORS,
  BALL_R,
  CUSHION_DEPTH,
  FOOT_SPOT,
  HEAD_SPOT,
  HEAD_STRING_X,
  TABLE_L,
  TABLE_W,
} from "../game/constants";
import { legalTargets } from "../game/rules";
import { TABLE } from "../game/table";
import type { Ball } from "../game/types";
import { MAX_PULL, type Match } from "../match";
import { BallSpinner, drawBall } from "./balls";
import { FRAME, fitView, toScreen, type View } from "./view";

/*
 * Canvas drawing for the table. The cloth, rails, cushions and pockets are
 * painted once per resize into an offscreen layer; every frame then copies
 * that layer and draws what moves on top: pocket flashes, the aiming guide,
 * the balls (as rolling spheres, in screen space so their digits stay
 * upright) and the cue.
 */

const CUE_LEN = 1460;
const STICK_TIP_GAP = 7;

export class TableRenderer {
  private canvas: HTMLCanvasElement;
  private ctx: CanvasRenderingContext2D;
  private layer: HTMLCanvasElement;
  private spinner = new BallSpinner();
  view: View;

  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("2D canvas unavailable");
    this.ctx = ctx;
    this.layer = document.createElement("canvas");
    this.view = fitView(1, 1, 1);
  }

  /** Match the backing store to the element's CSS size and repaint the table. */
  resize(cssW: number, cssH: number, dpr: number): void {
    const w = Math.max(1, Math.round(cssW * dpr));
    const h = Math.max(1, Math.round(cssH * dpr));
    this.view = fitView(cssW, cssH, dpr);
    this.canvas.width = w;
    this.canvas.height = h;
    this.layer.width = w;
    this.layer.height = h;
    const lctx = this.layer.getContext("2d");
    if (lctx) paintTable(lctx, this.view);
  }

  /** Forget ball orientations (a new rack). */
  resetSpin(): void {
    this.spinner = new BallSpinner();
  }

  draw(match: Match, now: number): void {
    const { ctx, view } = this;
    const dpr = view.dpr;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    ctx.drawImage(this.layer, 0, 0);

    const balls = match.balls();
    this.spinner.update(balls);

    this.tableSpace();
    drawFlashes(ctx, view, match, now);
    const game = match.game;
    const placing = match.canPlace() || match.dragging || match.phase === "cpu-place";
    if (placing && game.ballInHand === "kitchen") drawKitchen(ctx, view, now);

    // Balls dropping into pockets, then the balls on the table.
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    for (const d of match.drops) {
      if (d.id === 0 && match.phase !== "rolling") continue;
      const t = Math.min(1, (now - d.t0) / 260);
      const p = TABLE.pockets[d.pocket];
      const x = d.x + (p.x - d.x) * t;
      const y = d.y + (p.y - d.y) * t;
      const s = toScreen(view, x, y);
      drawBall(ctx, d.id, s.x, s.y, BALL_R * view.scale * (1 - 0.45 * t), this.spinner.orientation(d.id), view.rotated, dpr, 1 - t);
    }
    const cue = match.cuePosition();
    for (const b of balls) {
      if (b.pocket >= 0) continue;
      const at = b.id === 0 ? cue : b;
      const s = toScreen(view, at.x, at.y);
      drawBall(ctx, b.id, s.x, s.y, BALL_R * view.scale, this.spinner.orientation(b.id), view.rotated, dpr);
    }

    this.tableSpace();
    if (placing && balls[0].pocket < 0) drawHand(ctx, view, match, now);
    if (match.showGuide()) drawGuide(ctx, view, match, balls);
    if (match.showCue()) drawCue(ctx, view, match);
  }

  /** Switch the context to table millimetres. */
  private tableSpace(): void {
    const v = this.view;
    const k = v.dpr;
    this.ctx.setTransform(v.a * k, v.b * k, v.c * k, v.d * k, v.e * k, v.f * k);
  }
}

/* ------------------------------------------------------------ the table */

function tablePath(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function paintTable(ctx: CanvasRenderingContext2D, v: View): void {
  const k = v.dpr;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.clearRect(0, 0, ctx.canvas.width, ctx.canvas.height);
  ctx.setTransform(v.a * k, v.b * k, v.c * k, v.d * k, v.e * k, v.f * k);
  const px = 1 / v.scale; // one CSS pixel in mm

  // Frame: dark lacquer with a neon edge.
  const fx = -FRAME;
  const fw = TABLE_L + FRAME * 2;
  const fh = TABLE_W + FRAME * 2;
  ctx.save();
  ctx.shadowColor = "rgba(168, 85, 247, 0.55)";
  ctx.shadowBlur = 26 * k;
  tablePath(ctx, fx, fx, fw, fh, 54);
  const frame = ctx.createLinearGradient(0, -FRAME, 0, TABLE_W + FRAME);
  frame.addColorStop(0, "#1d1436");
  frame.addColorStop(0.5, "#120c26");
  frame.addColorStop(1, "#1a1230");
  ctx.fillStyle = frame;
  ctx.fill();
  ctx.restore();
  ctx.lineWidth = 2 * px;
  ctx.strokeStyle = "rgba(192, 132, 252, 0.75)";
  tablePath(ctx, fx + px, fx + px, fw - 2 * px, fh - 2 * px, 53);
  ctx.stroke();
  ctx.lineWidth = 1 * px;
  ctx.strokeStyle = "rgba(34, 211, 238, 0.25)";
  tablePath(ctx, -CUSHION_DEPTH - 14, -CUSHION_DEPTH - 14, TABLE_L + (CUSHION_DEPTH + 14) * 2, TABLE_W + (CUSHION_DEPTH + 14) * 2, 18);
  ctx.stroke();

  // Cloth.
  const cx = TABLE_L / 2;
  const cy = TABLE_W / 2;
  const cloth = ctx.createRadialGradient(cx, cy, 80, cx, cy, TABLE_L * 0.62);
  cloth.addColorStop(0, "#0f6170");
  cloth.addColorStop(0.55, "#0a4553");
  cloth.addColorStop(1, "#06262f");
  ctx.fillStyle = cloth;
  ctx.fillRect(-CUSHION_DEPTH, -CUSHION_DEPTH, TABLE_L + CUSHION_DEPTH * 2, TABLE_W + CUSHION_DEPTH * 2);
  // A faint weave so the cloth reads as cloth.
  ctx.strokeStyle = "rgba(255,255,255,0.018)";
  ctx.lineWidth = 3;
  for (let x = -CUSHION_DEPTH; x < TABLE_L + CUSHION_DEPTH; x += 14) {
    ctx.beginPath();
    ctx.moveTo(x, -CUSHION_DEPTH);
    ctx.lineTo(x + 40, TABLE_W + CUSHION_DEPTH);
    ctx.stroke();
  }

  // Markings: head string, spots.
  ctx.strokeStyle = "rgba(165, 243, 252, 0.14)";
  ctx.lineWidth = 1.5 * px;
  ctx.setLineDash([10, 12]);
  ctx.beginPath();
  ctx.moveTo(HEAD_STRING_X, 0);
  ctx.lineTo(HEAD_STRING_X, TABLE_W);
  ctx.stroke();
  ctx.setLineDash([]);
  ctx.fillStyle = "rgba(165, 243, 252, 0.35)";
  for (const s of [HEAD_SPOT, FOOT_SPOT, { x: TABLE_L / 2, y: TABLE_W / 2 }]) {
    ctx.beginPath();
    ctx.arc(s.x, s.y, 5, 0, Math.PI * 2);
    ctx.fill();
  }

  // Pockets: deep holes with a violet lip.
  for (const p of TABLE.pockets) {
    const g = ctx.createRadialGradient(p.x, p.y, p.holeR * 0.2, p.x, p.y, p.holeR * 1.05);
    g.addColorStop(0, "#000000");
    g.addColorStop(0.75, "#05030b");
    g.addColorStop(1, "#1a0f33");
    ctx.fillStyle = g;
    ctx.beginPath();
    ctx.arc(p.x, p.y, p.holeR, 0, Math.PI * 2);
    ctx.fill();
  }

  // Cushions, with a lit nose.
  for (const poly of TABLE.cushionPolys) {
    ctx.beginPath();
    poly.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)));
    ctx.closePath();
    ctx.fillStyle = "#0b5260";
    ctx.fill();
    ctx.strokeStyle = "rgba(4, 30, 38, 0.9)";
    ctx.lineWidth = 2 * px;
    ctx.stroke();
  }
  ctx.save();
  ctx.shadowColor = "rgba(34, 211, 238, 0.9)";
  ctx.shadowBlur = 8 * k;
  ctx.strokeStyle = "rgba(103, 232, 249, 0.8)";
  ctx.lineWidth = 1.5 * px;
  for (const s of TABLE.segments) {
    if (s.len < 300) continue; // the long noses only
    ctx.beginPath();
    ctx.moveTo(s.ax, s.ay);
    ctx.lineTo(s.bx, s.by);
    ctx.stroke();
  }
  ctx.restore();

  // Pocket rims over the cushion ends.
  for (const p of TABLE.pockets) {
    ctx.strokeStyle = "rgba(192, 132, 252, 0.55)";
    ctx.lineWidth = 2 * px;
    ctx.beginPath();
    ctx.arc(p.x, p.y, p.holeR + 1, 0, Math.PI * 2);
    ctx.stroke();
  }

  // Diamonds on the rails.
  ctx.save();
  ctx.shadowColor = "rgba(34, 211, 238, 0.9)";
  ctx.shadowBlur = 6 * k;
  ctx.fillStyle = "rgba(165, 243, 252, 0.85)";
  const railMid = -(CUSHION_DEPTH + FRAME) / 2 - 8;
  const diamond = (x: number, y: number) => {
    const d = 7;
    ctx.beginPath();
    ctx.moveTo(x, y - d);
    ctx.lineTo(x + d, y);
    ctx.lineTo(x, y + d);
    ctx.lineTo(x - d, y);
    ctx.closePath();
    ctx.fill();
  };
  for (let i = 1; i < 8; i++) {
    if (i === 4) continue;
    const x = (TABLE_L * i) / 8;
    diamond(x, railMid);
    diamond(x, TABLE_W - railMid);
  }
  for (let i = 1; i < 4; i++) {
    const y = (TABLE_W * i) / 4;
    diamond(railMid, y);
    diamond(TABLE_L - railMid, y);
  }
  ctx.restore();
}

/* ---------------------------------------------------------- per frame */

/** A neon ring that swells out of the pocket rim when a ball drops. */
function drawFlashes(ctx: CanvasRenderingContext2D, v: View, match: Match, now: number) {
  for (const f of match.flashes) {
    const t = (now - f.t0) / 900;
    if (t < 0 || t > 1) continue;
    const p = TABLE.pockets[f.pocket];
    const color = f.id === 8 ? "#c084fc" : f.id === 0 ? "#e2e8f0" : BALL_COLORS[f.id];
    const ease = 1 - (1 - t) * (1 - t);
    ctx.save();
    ctx.globalAlpha = 1 - t;
    ctx.shadowColor = color;
    ctx.shadowBlur = 22 * v.dpr;
    ctx.strokeStyle = color;
    ctx.lineWidth = (4.5 - 3 * t) / v.scale;
    ctx.beginPath();
    ctx.arc(p.x, p.y, p.holeR * (1.02 + 0.55 * ease), 0, Math.PI * 2);
    ctx.stroke();
    ctx.globalAlpha = 0.6 * (1 - t);
    ctx.lineWidth = 2 / v.scale;
    ctx.beginPath();
    ctx.arc(p.x, p.y, p.holeR * 1.02, 0, Math.PI * 2);
    ctx.stroke();
    ctx.restore();
  }
}

function drawKitchen(ctx: CanvasRenderingContext2D, v: View, now: number) {
  const pulse = 0.5 + 0.5 * Math.sin(now / 380);
  ctx.fillStyle = `rgba(34, 211, 238, ${0.05 + 0.03 * pulse})`;
  ctx.fillRect(0, 0, HEAD_STRING_X, TABLE_W);
  ctx.strokeStyle = `rgba(103, 232, 249, ${0.45 + 0.25 * pulse})`;
  ctx.lineWidth = 2 / v.scale;
  ctx.setLineDash([14, 10]);
  ctx.beginPath();
  ctx.moveTo(HEAD_STRING_X, 0);
  ctx.lineTo(HEAD_STRING_X, TABLE_W);
  ctx.stroke();
  ctx.setLineDash([]);
}

function drawHand(ctx: CanvasRenderingContext2D, v: View, match: Match, now: number) {
  const c = match.cuePosition();
  const bad = match.dragging && !match.dragValid;
  const pulse = 0.5 + 0.5 * Math.sin(now / 260);
  ctx.strokeStyle = bad ? "rgba(251, 113, 133, 0.95)" : `rgba(165, 243, 252, ${0.55 + 0.35 * pulse})`;
  ctx.lineWidth = 2 / v.scale;
  ctx.setLineDash([9, 7]);
  ctx.beginPath();
  ctx.arc(c.x, c.y, BALL_R * (1.75 + (match.dragging ? 0 : 0.15 * pulse)), 0, Math.PI * 2);
  ctx.stroke();
  ctx.setLineDash([]);
}

function drawGuide(ctx: CanvasRenderingContext2D, v: View, match: Match, balls: readonly Ball[]) {
  const g = aimGuide(balls, match.aim);
  const cue = balls[0];
  const px = 1 / v.scale;
  const human = match.isHumanTurn();
  const alpha = human ? 1 : 0.55;
  const legal = g.kind === "ball" && legalTargets(match.game).includes(g.targetId);
  const accent = g.kind === "ball" ? (legal ? "34, 211, 238" : "251, 113, 133") : "226, 232, 240";

  ctx.save();
  ctx.lineCap = "round";
  // Cue-ball path.
  ctx.strokeStyle = `rgba(241, 245, 249, ${0.7 * alpha})`;
  ctx.lineWidth = 1.6 * px;
  ctx.setLineDash([7 * px, 6 * px]);
  ctx.beginPath();
  ctx.moveTo(cue.x, cue.y);
  ctx.lineTo(g.end.x, g.end.y);
  ctx.stroke();
  ctx.setLineDash([]);

  // Ghost ball at the first contact.
  ctx.strokeStyle = g.kind === "pocket" ? `rgba(251, 113, 133, ${0.9 * alpha})` : `rgba(${accent}, ${0.9 * alpha})`;
  ctx.lineWidth = 1.6 * px;
  ctx.beginPath();
  ctx.arc(g.end.x, g.end.y, BALL_R, 0, Math.PI * 2);
  ctx.stroke();
  ctx.fillStyle = `rgba(${accent}, ${0.1 * alpha})`;
  ctx.fill();

  if (g.kind === "ball" && g.objectDir) {
    const obj = balls[g.targetId];
    // Project the object ball's line up to whatever it would meet first.
    const ahead = sweep(balls, obj.x, obj.y, g.objectDir.x, g.objectDir.y, g.targetId, 0, true);
    const len = Math.min(140 + 520 * Math.cos(g.cut), ahead.dist);
    const ex = obj.x + g.objectDir.x * len;
    const ey = obj.y + g.objectDir.y * len;
    ctx.save();
    ctx.shadowColor = `rgba(${accent}, 0.8)`;
    ctx.shadowBlur = 6 * v.dpr;
    ctx.strokeStyle = `rgba(${accent}, ${0.95 * alpha})`;
    ctx.lineWidth = 2.2 * px;
    ctx.beginPath();
    ctx.moveTo(obj.x + g.objectDir.x * BALL_R, obj.y + g.objectDir.y * BALL_R);
    ctx.lineTo(ex, ey);
    ctx.stroke();
    if (ahead.kind === "pocket" && len >= ahead.dist - 1) {
      // Straight in: ring the pocket it is heading for.
      const p = TABLE.pockets[ahead.id];
      ctx.lineWidth = 2 * px;
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.holeR * 0.82, 0, Math.PI * 2);
      ctx.stroke();
    } else if (len > BALL_R * 2) {
      const ah = 11 * px + 8;
      const nx = -g.objectDir.y;
      const ny = g.objectDir.x;
      ctx.beginPath();
      ctx.moveTo(ex + g.objectDir.x * ah * 0.6, ey + g.objectDir.y * ah * 0.6);
      ctx.lineTo(ex - g.objectDir.x * ah * 0.6 + nx * ah * 0.55, ey - g.objectDir.y * ah * 0.6 + ny * ah * 0.55);
      ctx.lineTo(ex - g.objectDir.x * ah * 0.6 - nx * ah * 0.55, ey - g.objectDir.y * ah * 0.6 - ny * ah * 0.55);
      ctx.closePath();
      ctx.fillStyle = `rgba(${accent}, ${0.95 * alpha})`;
      ctx.fill();
    }
    ctx.restore();
  }
  if (g.cueDir && (g.kind === "ball" || g.kind === "cushion")) {
    const target = g.kind === "ball" ? g.targetId : -1;
    const ahead = sweep(balls, g.end.x, g.end.y, g.cueDir.x, g.cueDir.y, 0, target, true);
    const len = Math.min(g.kind === "ball" ? 60 + 220 * Math.sin(g.cut) : 240, ahead.dist);
    ctx.strokeStyle = `rgba(241, 245, 249, ${0.42 * alpha})`;
    ctx.lineWidth = 1.4 * px;
    ctx.setLineDash([5 * px, 6 * px]);
    ctx.beginPath();
    ctx.moveTo(g.end.x, g.end.y);
    ctx.lineTo(g.end.x + g.cueDir.x * len, g.end.y + g.cueDir.y * len);
    ctx.stroke();
    ctx.setLineDash([]);
  }
  ctx.restore();
}

function drawCue(ctx: CanvasRenderingContext2D, v: View, match: Match) {
  const c = match.cuePosition();
  const dx = Math.cos(match.aim);
  const dy = Math.sin(match.aim);
  const back = BALL_R + STICK_TIP_GAP + match.pull * MAX_PULL;
  const tipX = c.x - dx * back;
  const tipY = c.y - dy * back;
  const buttX = tipX - dx * CUE_LEN;
  const buttY = tipY - dy * CUE_LEN;
  const nx = -dy;
  const ny = dx;
  const tipW = 6;
  const buttW = 14;
  const alpha = match.phase === "rolling" ? match.stickAlpha : 1;
  if (alpha <= 0.01) return;

  ctx.save();
  ctx.globalAlpha = alpha;
  const g = ctx.createLinearGradient(tipX, tipY, buttX, buttY);
  g.addColorStop(0, "#60a5fa"); // chalked tip
  g.addColorStop(0.01, "#60a5fa");
  g.addColorStop(0.012, "#f8fafc"); // ferrule
  g.addColorStop(0.03, "#f8fafc");
  g.addColorStop(0.032, "#f1dfbd"); // maple shaft
  g.addColorStop(0.55, "#e5c99a");
  g.addColorStop(0.56, "#22d3ee"); // neon joint
  g.addColorStop(0.575, "#22d3ee");
  g.addColorStop(0.585, "#2a1650"); // butt
  g.addColorStop(0.9, "#3b1d73");
  g.addColorStop(0.91, "#c084fc");
  g.addColorStop(0.93, "#c084fc");
  g.addColorStop(0.94, "#160c2c");
  g.addColorStop(1, "#160c2c");
  ctx.shadowColor = "rgba(34, 211, 238, 0.55)";
  ctx.shadowBlur = 10 * v.dpr;
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.moveTo(tipX + nx * tipW, tipY + ny * tipW);
  ctx.lineTo(buttX + nx * buttW, buttY + ny * buttW);
  ctx.lineTo(buttX - nx * buttW, buttY - ny * buttW);
  ctx.lineTo(tipX - nx * tipW, tipY - ny * tipW);
  ctx.closePath();
  ctx.fill();
  ctx.restore();
}
