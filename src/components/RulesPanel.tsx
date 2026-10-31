"use client";

import { useEffect } from "react";

interface RulesPanelProps {
  onClose: () => void;
}

export function RulesPanel({ onClose }: RulesPanelProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="bl-overlay" role="dialog" aria-modal aria-label="How to play">
      <div className="bl-dialog is-wide">
        <h2 className="bl-dialog-title">How to play</h2>
        <div className="bl-rules">
          <p>
            <b>8-ball.</b> One player takes the <b>solids</b> (1–7), the other the <b>stripes</b> (9–15). Pocket all
            seven of yours, then sink the <b>8</b> to win.
          </p>
          <p>
            <b>The break.</b> Place the cue ball anywhere behind the line and smash the rack. Pocket anything on a
            legal break and you keep shooting; the table stays open either way. An 8 dropped on the break is spotted
            again.
          </p>
          <p>
            <b>Groups.</b> On the open table you may hit any ball but the 8 first. The first ball you pocket legally
            makes that group yours. Pocket one of your own and you shoot again.
          </p>
          <p>
            <b>Fouls</b> give your opponent <b>ball in hand</b> — they put the cue ball anywhere free: pocketing the cue
            ball (a scratch), missing every ball, hitting the wrong ball first (your group, or the 8 once your group is
            gone), or touching a ball without any ball reaching a cushion or a pocket. After a scratch on the break the
            cue ball goes behind the line. Balls pocketed on a foul stay down.
          </p>
          <p>
            <b>The 8.</b> Sink it after clearing your group to win. Sink it early, or on a foul, and you lose. No need
            to call the pocket.
          </p>
          <p>
            <b>Controls.</b> Mouse: point to aim, press and pull back to draw the cue, release to shoot — or drag the
            power bar and let go. Touch: drag on the table to aim, set the power slider, tap <b>Shoot</b>. The spin pad
            adds <b>follow</b> (top) or <b>draw</b> (bottom). Keys: <b>←/→</b> aim (<b>Shift</b> for fine),{" "}
            <b>↑/↓</b> spin, hold <b>Space</b> to charge and release to shoot, <b>WASD</b> moves a ball in hand.
          </p>
        </div>
        <button type="button" className="bl-primary" onClick={onClose} autoFocus>
          Got it
        </button>
      </div>
    </div>
  );
}
