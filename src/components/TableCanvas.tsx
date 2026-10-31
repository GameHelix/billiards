"use client";

import { useEffect, useRef, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import type { Match } from "@/lib/match";
import { TableRenderer } from "@/lib/render/renderer";
import { toTable } from "@/lib/render/view";

interface TableCanvasProps {
  match: Match;
  paused: boolean;
  /** Overlays (toasts, hints) laid over the table. */
  children?: ReactNode;
}

/** CSS pixels of pull-back for a full-power stroke with the mouse. */
const FULL_PULL_PX = 190;

type Gesture = { mode: "none" | "drag" | "charge" | "aim"; startX: number; startY: number; pointer: number };

/**
 * The table canvas. One animation loop advances the match and repaints;
 * pointer input becomes intents on the match:
 *   · mouse — hover to aim, press and pull back to charge, release to shoot;
 *   · touch — drag to aim (the power slider and Shoot button do the rest);
 *   · ball in hand — press on the cue ball and drag it to a free spot.
 */
export function TableCanvas({ match, paused, children }: TableCanvasProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const rendererRef = useRef<TableRenderer | null>(null);
  const pausedRef = useRef(paused);
  const gesture = useRef<Gesture>({ mode: "none", startX: 0, startY: 0, pointer: -1 });

  useEffect(() => {
    pausedRef.current = paused;
  }, [paused]);

  useEffect(() => {
    const canvas = canvasRef.current;
    const wrap = wrapRef.current;
    if (!canvas || !wrap) return;
    const renderer = new TableRenderer(canvas);
    rendererRef.current = renderer;

    const resize = () => {
      const r = wrap.getBoundingClientRect();
      renderer.resize(r.width, r.height, Math.min(window.devicePixelRatio || 1, 2.5));
    };
    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(wrap);

    let raf = 0;
    const loop = (t: number) => {
      if (!pausedRef.current) match.frame(t);
      renderer.draw(match, t);
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      rendererRef.current = null;
    };
  }, [match]);

  const tablePoint = (e: ReactPointerEvent) => {
    const r = rendererRef.current;
    const c = canvasRef.current;
    if (!r || !c) return null;
    const rect = c.getBoundingClientRect();
    return { p: toTable(r.view, e.clientX - rect.left, e.clientY - rect.top), scale: r.view.scale };
  };

  const setCursor = (cursor: string) => {
    const c = canvasRef.current;
    if (c && c.style.cursor !== cursor) c.style.cursor = cursor;
  };

  const onPointerDown = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (pausedRef.current) return;
    if (e.pointerType === "mouse" && e.button !== 0) return;
    const hit = tablePoint(e);
    if (!hit) return;
    const g = gesture.current;
    const { p } = hit;
    if (match.canPlace() && match.nearCueBall(p.x, p.y, e.pointerType === "mouse" ? 1.6 : 2.6)) {
      if (match.grabCueBall()) {
        g.mode = "drag";
        g.pointer = e.pointerId;
        e.currentTarget.setPointerCapture(e.pointerId);
        setCursor("grabbing");
      }
      return;
    }
    if (!match.canAim()) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    g.pointer = e.pointerId;
    if (e.pointerType === "mouse") {
      if (match.startCharge("pointer")) {
        g.mode = "charge";
        g.startX = p.x;
        g.startY = p.y;
      }
    } else {
      g.mode = "aim";
      if (!match.nearCueBall(p.x, p.y, 2)) match.aimAt(p.x, p.y);
    }
  };

  const onPointerMove = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const hit = tablePoint(e);
    if (!hit) return;
    const { p, scale } = hit;
    const g = gesture.current;
    switch (g.mode) {
      case "drag":
        match.dragCueBall(p.x, p.y);
        break;
      case "charge": {
        // Pulling the mouse back against the shot direction draws the cue.
        const back = (g.startX - p.x) * Math.cos(match.aim) + (g.startY - p.y) * Math.sin(match.aim);
        match.setCharge((back * scale) / FULL_PULL_PX);
        break;
      }
      case "aim":
        if (!match.nearCueBall(p.x, p.y, 2)) match.aimAt(p.x, p.y);
        break;
      default:
        if (e.pointerType === "mouse") {
          if (match.canPlace() && match.nearCueBall(p.x, p.y, 1.6)) setCursor("grab");
          else {
            setCursor(match.canAim() ? "crosshair" : "default");
            match.aimAt(p.x, p.y);
          }
        }
    }
  };

  const endGesture = (e: ReactPointerEvent<HTMLCanvasElement>, cancelled: boolean) => {
    const g = gesture.current;
    if (g.pointer !== e.pointerId) return;
    if (g.mode === "drag") {
      match.dropCueBall();
      setCursor("grab");
    } else if (g.mode === "charge") {
      if (cancelled) match.cancelCharge();
      else match.releaseCharge();
    }
    g.mode = "none";
    g.pointer = -1;
  };

  return (
    <div ref={wrapRef} className="bl-table-wrap">
      <canvas
        ref={canvasRef}
        className="bl-canvas"
        aria-label="Pool table. Aim with the mouse or by dragging; pull back and release, or use the power control, to shoot."
        role="img"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={(e) => endGesture(e, false)}
        onPointerCancel={(e) => endGesture(e, true)}
        onLostPointerCapture={(e) => endGesture(e, true)}
        onContextMenu={(e) => {
          e.preventDefault();
          match.cancelCharge();
        }}
      />
      {children}
    </div>
  );
}
