"use client";

import type { Prefs, Stats } from "./BilliardsGame";
import { cx } from "@/lib/cx";
import { DIFFICULTIES, DIFFICULTY_LABEL } from "@/lib/game/constants";
import type { Mode } from "@/lib/game/types";

interface MenuScreenProps {
  prefs: Prefs;
  stats: Stats;
  onPrefs: (prefs: Prefs) => void;
  onStart: () => void;
  onRules: () => void;
}

const MODES: { id: Mode; label: string }[] = [
  { id: "cpu", label: "vs CPU" },
  { id: "local", label: "2 players" },
];

export function MenuScreen({ prefs, stats, onPrefs, onStart, onRules }: MenuScreenProps) {
  return (
    <>
      <h1 className="bl-title">{"🎱 BILLIARDS"}</h1>
      <p className="bl-subtitle">8-ball pool on neon felt. Break, run your group, sink the 8.</p>

      <div className="bl-card">
        <span className="bl-field-label">Opponent</span>
        <div className="bl-choice-row is-two">
          {MODES.map((m) => (
            <button
              key={m.id}
              type="button"
              className={cx("bl-choice", prefs.mode === m.id && "is-on")}
              aria-pressed={prefs.mode === m.id}
              onClick={() => onPrefs({ ...prefs, mode: m.id })}
            >
              {m.label}
            </button>
          ))}
        </div>

        {prefs.mode === "cpu" ? (
          <>
            <span className="bl-field-label">Difficulty</span>
            <div className="bl-choice-row">
              {DIFFICULTIES.map((d) => (
                <button
                  key={d}
                  type="button"
                  className={cx("bl-choice", prefs.difficulty === d && "is-on")}
                  aria-pressed={prefs.difficulty === d}
                  onClick={() => onPrefs({ ...prefs, difficulty: d })}
                >
                  {DIFFICULTY_LABEL[d]}
                </button>
              ))}
            </div>
          </>
        ) : (
          <p className="bl-menu-note">Two players share the cue, taking turns at the table.</p>
        )}

        <button type="button" className="bl-primary" onClick={onStart}>
          Rack &apos;em
        </button>

        <div className="bl-record" aria-label="Your record against the CPU">
          {DIFFICULTIES.map((d) => {
            const r = stats[d] ?? { wins: 0, losses: 0 };
            return (
              <div key={d} className={cx("bl-record-cell", prefs.mode === "cpu" && prefs.difficulty === d && "is-on")}>
                <span className="bl-record-tier">{DIFFICULTY_LABEL[d]}</span>
                <span className="bl-record-wins">{r.wins}</span>
                <span className="bl-record-sub">{`win${r.wins === 1 ? "" : "s"} · ${r.losses} lost`}</span>
              </div>
            );
          })}
        </div>
        {stats.duels > 0 ? (
          <p className="bl-menu-note">{`${stats.duels} two-player game${stats.duels === 1 ? "" : "s"} played`}</p>
        ) : null}

        <div className="bl-menu-actions">
          <button type="button" className="bl-ghost" onClick={onRules}>
            How to play
          </button>
          <button
            type="button"
            className="bl-ghost"
            aria-pressed={prefs.sound}
            onClick={() => onPrefs({ ...prefs, sound: !prefs.sound })}
          >
            {prefs.sound ? "🔊 Sound on" : "🔇 Sound off"}
          </button>
        </div>
      </div>
    </>
  );
}
