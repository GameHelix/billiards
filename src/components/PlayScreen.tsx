"use client";

import { useEffect, useState, useSyncExternalStore } from "react";
import { Hud } from "./Hud";
import { ShotControls } from "./ShotControls";
import { TableCanvas } from "./TableCanvas";
import type { GameState } from "@/lib/game/rules";
import type { Difficulty, Mode, PlayerIndex } from "@/lib/game/types";
import { Match, type Voice } from "@/lib/match";

interface PlayScreenProps {
  seed: number;
  breaker: PlayerIndex;
  mode: Mode;
  difficulty: Difficulty;
  names: [string, string];
  soundOn: boolean;
  play: (voice: Voice, magnitude?: number) => void;
  /** Freeze the table (a dialog is open over it). */
  paused: boolean;
  onGameOver: (game: GameState) => void;
  onNewGame: () => void;
  onMenu: () => void;
  onRules: () => void;
  onToggleSound: () => void;
}

const DEG = Math.PI / 180;

/** Leave keys alone while the person is typing in a field. */
function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.closest("input, textarea, select") !== null;
}

export function PlayScreen({
  seed,
  breaker,
  mode,
  difficulty,
  names,
  soundOn,
  play,
  paused,
  onGameOver,
  onNewGame,
  onMenu,
  onRules,
  onToggleSound,
}: PlayScreenProps) {
  // One match per mount; the parent remounts this screen for a new game.
  const [match] = useState(
    () => new Match({ mode, difficulty, seed, breaker, names, clock: () => performance.now() })
  );
  const view = useSyncExternalStore(match.subscribe, match.getView, match.getView);

  useEffect(() => {
    match.connect({ sound: play, over: onGameOver });
  }, [match, play, onGameOver]);

  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const apply = () => match.setReducedMotion(mq.matches);
    apply();
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, [match]);

  useEffect(() => {
    match.start();
  }, [match]);

  // Keyboard: ←/→ aim (Shift: fine), ↑/↓ follow and draw, Space charges and
  // shoots on release, Enter shoots at the meter's power, WASD moves the cue
  // ball while it is in hand, Esc puts the cue down.
  useEffect(() => {
    if (paused) return;
    const held = new Set<string>();
    const syncRotation = (shift: boolean) => {
      const dir = (held.has("ArrowRight") ? 1 : 0) - (held.has("ArrowLeft") ? 1 : 0);
      match.setRotation(dir, shift);
    };
    const onDown = (e: KeyboardEvent) => {
      if (isTyping(e.target) || e.metaKey || e.ctrlKey || e.altKey) return;
      const k = e.key;
      if (k === "ArrowLeft" || k === "ArrowRight") {
        e.preventDefault();
        if (!e.repeat) {
          match.nudgeAim((k === "ArrowRight" ? 1 : -1) * (e.shiftKey ? 0.05 : 0.5) * DEG);
          held.add(k);
          syncRotation(e.shiftKey);
        }
      } else if (k === "ArrowUp" || k === "ArrowDown") {
        e.preventDefault();
        match.setSpin(Math.round((match.spin + (k === "ArrowUp" ? 0.25 : -0.25)) * 4) / 4);
      } else if (k === " " || k === "Spacebar") {
        if (e.target instanceof HTMLButtonElement) return;
        e.preventDefault();
        if (!e.repeat) match.startCharge("key");
      } else if (k === "Enter") {
        if (e.target instanceof HTMLButtonElement) return;
        e.preventDefault();
        match.shoot();
      } else if (k === "Escape") {
        match.cancelCharge();
      } else if ("wasdWASD".includes(k) && k.length === 1) {
        const step = e.shiftKey ? 2 : 10;
        const lower = k.toLowerCase();
        match.moveCueBall(lower === "a" ? -step : lower === "d" ? step : 0, lower === "w" ? -step : lower === "s" ? step : 0);
      }
    };
    const onUp = (e: KeyboardEvent) => {
      const k = e.key;
      if (k === "ArrowLeft" || k === "ArrowRight") {
        held.delete(k);
        syncRotation(e.shiftKey);
      } else if (k === " " || k === "Spacebar") {
        match.releaseCharge();
      } else if (k === "Shift") {
        syncRotation(false);
      }
    };
    const onBlur = () => {
      held.clear();
      match.setRotation(0, false);
      match.cancelCharge();
    };
    window.addEventListener("keydown", onDown);
    window.addEventListener("keyup", onUp);
    window.addEventListener("blur", onBlur);
    return () => {
      window.removeEventListener("keydown", onDown);
      window.removeEventListener("keyup", onUp);
      window.removeEventListener("blur", onBlur);
      match.setRotation(0, false);
    };
  }, [match, paused]);

  const cpuTurn = mode === "cpu" && view.game.turn === 1 && view.game.winner === null;

  return (
    <div className="bl-play">
      <Hud view={view} names={names} mode={mode} difficulty={difficulty} />

      <div className="bl-stage">
        <TableCanvas match={match} paused={paused}>
          {view.toast ? (
            <div key={view.toast.id} className={`bl-toast is-${view.toast.tone}`} role="status" aria-live="polite">
              <span className="bl-toast-title">{view.toast.title}</span>
              {view.toast.detail ? <span className="bl-toast-detail">{view.toast.detail}</span> : null}
            </div>
          ) : null}
          {view.thinking ? (
            <div className="bl-thinking" aria-live="polite">
              CPU thinking<span className="bl-dots" aria-hidden />
            </div>
          ) : null}
          {view.humanTurn && view.game.ballInHand !== "none" && !view.dragging ? (
            <div className="bl-hint">
              {view.game.ballInHand === "kitchen" ? "Ball in hand · behind the line" : "Ball in hand · drag the cue ball"}
            </div>
          ) : null}
        </TableCanvas>

        <aside className="bl-side">
          <ShotControls match={match} view={view} disabled={!view.humanTurn || paused} cpuTurn={cpuTurn} />
          <div className="bl-bar">
            <button type="button" className="bl-ghost is-small" onClick={onMenu}>
              Menu
            </button>
            <button type="button" className="bl-ghost is-small" onClick={onNewGame} aria-label="New game">
              New
            </button>
            <button type="button" className="bl-ghost is-small" onClick={onRules}>
              Rules
            </button>
            <button type="button" className="bl-ghost is-small" aria-pressed={soundOn} onClick={onToggleSound}>
              {soundOn ? "🔊" : "🔇"}
              <span className="bl-sr">{soundOn ? "Sound on" : "Sound off"}</span>
            </button>
          </div>
        </aside>
      </div>

      <p className="bl-keys">
        Mouse: aim · press &amp; pull back, release to shoot · ←/→ aim (Shift fine) · ↑/↓ follow/draw · hold Space to
        charge · WASD moves a ball in hand
      </p>
    </div>
  );
}
