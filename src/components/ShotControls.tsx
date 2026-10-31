"use client";

import { useEffect, useRef, type CSSProperties, type KeyboardEvent, type PointerEvent } from "react";
import { cx } from "@/lib/cx";
import type { Match, MatchView } from "@/lib/match";

interface ShotControlsProps {
  match: Match;
  view: MatchView;
  disabled: boolean;
  cpuTurn: boolean;
}

const DEG = Math.PI / 180;

/**
 * Power, spin, fine aim and the Shoot button. The power meter is a bar you
 * drag: with a mouse, releasing it shoots (like letting the cue go); with a
 * finger it just sets the power and the Shoot button strikes, so a shot is
 * never fired by accident. Laid out as a column beside a landscape table and
 * as a row under a portrait one.
 */
export function ShotControls({ match, view, disabled, cpuTurn }: ShotControlsProps) {
  return (
    <div className={cx("bl-controls", disabled && "is-idle")}>
      <SpinPad match={match} spin={view.spin} disabled={disabled} />
      <PowerMeter match={match} power={view.power} charging={view.charging} disabled={disabled} />
      <div className="bl-nudges" role="group" aria-label="Fine aim">
        <NudgeButton match={match} dir={-1} disabled={disabled} />
        <NudgeButton match={match} dir={1} disabled={disabled} />
      </div>
      <button
        type="button"
        className="bl-shoot"
        disabled={disabled}
        onClick={(e) => {
          match.shoot();
          // A mouse click should not leave Space bound to this button: on the
          // table, Space charges the cue.
          if (e.detail > 0) e.currentTarget.blur();
        }}
      >
        {cpuTurn ? "CPU" : "Shoot"}
      </button>
    </div>
  );
}

function PowerMeter({
  match,
  power,
  charging,
  disabled,
}: {
  match: Match;
  power: number;
  charging: boolean;
  disabled: boolean;
}) {
  const trackRef = useRef<HTMLDivElement>(null);
  const mode = useRef<"none" | "charge" | "set">("none");

  /** 0..1 along the bar: downward on a vertical bar (pulling the cue back), rightward on a flat one. */
  const valueAt = (e: PointerEvent) => {
    const el = trackRef.current;
    if (!el) return 0;
    const r = el.getBoundingClientRect();
    const v = r.height > r.width ? (e.clientY - r.top) / r.height : (e.clientX - r.left) / r.width;
    return Math.min(1, Math.max(0, v));
  };

  const onDown = (e: PointerEvent<HTMLDivElement>) => {
    if (disabled) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    if (e.pointerType === "mouse") {
      if (match.startCharge("meter")) {
        mode.current = "charge";
        match.setCharge(valueAt(e));
      }
    } else {
      mode.current = "set";
      match.setPower(valueAt(e));
    }
  };
  const onMove = (e: PointerEvent<HTMLDivElement>) => {
    if (mode.current === "charge") match.setCharge(valueAt(e));
    else if (mode.current === "set") match.setPower(valueAt(e));
  };
  const onUp = () => {
    if (mode.current === "charge") match.releaseCharge();
    mode.current = "none";
  };
  const onCancel = () => {
    if (mode.current === "charge") match.cancelCharge();
    mode.current = "none";
  };
  // Arrows adjust the focused meter instead of aiming; every other key
  // (Space to charge, Enter to shoot) still reaches the table.
  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (disabled) return;
    const step = e.shiftKey ? 0.01 : 0.05;
    if (e.key === "ArrowUp" || e.key === "ArrowRight") match.setPower(power + step);
    else if (e.key === "ArrowDown" || e.key === "ArrowLeft") match.setPower(power - step);
    else return;
    e.preventDefault();
    e.stopPropagation();
  };

  const pct = Math.round(power * 100);
  return (
    <div className="bl-power">
      <span className="bl-power-label">Power</span>
      <div
        ref={trackRef}
        className={cx("bl-power-track", charging && "is-charging", disabled && "is-disabled")}
        style={{ "--p": power } as CSSProperties}
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-label="Shot power"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
        aria-disabled={disabled}
        onPointerDown={onDown}
        onPointerMove={onMove}
        onPointerUp={onUp}
        onPointerCancel={onCancel}
        onKeyDown={onKey}
      >
        <div className="bl-power-fill" />
        <div className="bl-power-knob" />
      </div>
      <span className="bl-power-value">{`${pct}%`}</span>
    </div>
  );
}

function SpinPad({ match, spin, disabled }: { match: Match; spin: number; disabled: boolean }) {
  const padRef = useRef<HTMLDivElement>(null);
  const active = useRef(false);

  const spinAt = (e: PointerEvent) => {
    const el = padRef.current;
    if (!el) return 0;
    const r = el.getBoundingClientRect();
    const v = (r.top + r.height / 2 - e.clientY) / (r.height * 0.36);
    // Snap gently to centre ball so a stun is easy to find again.
    const s = Math.max(-1, Math.min(1, v));
    return Math.abs(s) < 0.12 ? 0 : Math.round(s * 20) / 20;
  };
  const onDown = (e: PointerEvent<HTMLDivElement>) => {
    if (disabled) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    active.current = true;
    match.setSpin(spinAt(e));
  };
  const onMove = (e: PointerEvent<HTMLDivElement>) => {
    if (active.current) match.setSpin(spinAt(e));
  };
  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (disabled) return;
    if (e.key === "ArrowUp") match.setSpin(spin + 0.25);
    else if (e.key === "ArrowDown") match.setSpin(spin - 0.25);
    else if (e.key === "0" || e.key === "Home") match.setSpin(0);
    else return;
    e.preventDefault();
    e.stopPropagation();
  };

  const label = spin > 0.05 ? "Follow" : spin < -0.05 ? "Draw" : "Centre";
  return (
    <div className="bl-spin">
      <div
        ref={padRef}
        className={cx("bl-spin-pad", disabled && "is-disabled")}
        style={{ "--s": spin } as CSSProperties}
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-label="Spin: follow or draw"
        aria-valuemin={-100}
        aria-valuemax={100}
        aria-valuenow={Math.round(spin * 100)}
        aria-valuetext={label}
        aria-disabled={disabled}
        onPointerDown={onDown}
        onPointerMove={onMove}
        onPointerUp={() => (active.current = false)}
        onPointerCancel={() => (active.current = false)}
        onDoubleClick={() => !disabled && match.setSpin(0)}
        onKeyDown={onKey}
      >
        <span className="bl-spin-dot" />
      </div>
      <span className="bl-spin-label">{label}</span>
    </div>
  );
}

/** ◀ / ▶ : a tap turns the aim a hair; holding keeps turning, faster. */
function NudgeButton({ match, dir, disabled }: { match: Match; dir: -1 | 1; disabled: boolean }) {
  const timer = useRef<number | null>(null);
  const stop = () => {
    if (timer.current !== null) window.clearInterval(timer.current);
    timer.current = null;
  };
  useEffect(() => stop, []);
  const start = (e: PointerEvent<HTMLButtonElement>) => {
    if (disabled) return;
    e.preventDefault();
    stop();
    match.nudgeAim(dir * 0.1 * DEG);
    let n = 0;
    timer.current = window.setInterval(() => {
      n += 1;
      if (n > 4) match.nudgeAim(dir * (n > 20 ? 0.5 : 0.15) * DEG);
    }, 60);
  };
  return (
    <button
      type="button"
      className="bl-nudge"
      disabled={disabled}
      aria-label={dir < 0 ? "Turn aim left" : "Turn aim right"}
      onPointerDown={start}
      onPointerUp={stop}
      onPointerLeave={stop}
      onPointerCancel={stop}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          match.nudgeAim(dir * 0.1 * DEG);
        }
      }}
    >
      {dir < 0 ? "◀" : "▶"}
    </button>
  );
}
