"use client";

import type { CSSProperties } from "react";
import { cx } from "@/lib/cx";
import { BALL_COLORS, DIFFICULTY_LABEL } from "@/lib/game/constants";
import { groupBallIds } from "@/lib/game/rules";
import { isStripe } from "@/lib/game/table";
import type { Difficulty, Mode, PlayerIndex } from "@/lib/game/types";
import type { MatchView } from "@/lib/match";

interface HudProps {
  view: MatchView;
  names: [string, string];
  mode: Mode;
  difficulty: Difficulty;
}

/** A flat little ball for the HUD: colour, stripe band and number. */
export function MiniBall({ id, dim = false }: { id: number; dim?: boolean }) {
  return (
    <span
      className={cx("bl-mini", isStripe(id) && "is-stripe", id === 8 && "is-eight", dim && "is-dim")}
      style={{ "--c": BALL_COLORS[id] } as CSSProperties}
      aria-label={dim ? `${id}, pocketed` : String(id)}
    >
      <span className="bl-mini-num">{id}</span>
    </span>
  );
}

/**
 * Both players side by side — the one at the table glows — with their group,
 * its balls (pocketed ones dimmed) and, below, the tray of every ball
 * pocketed so far in the order they dropped.
 */
export function Hud({ view, names, mode, difficulty }: HudProps) {
  const g = view.game;
  const down = new Set(view.tray);
  const players: PlayerIndex[] = [0, 1];

  return (
    <header className="bl-hud">
      <div className="bl-players">
        {players.map((p) => {
          const group = g.groups[p];
          const active = g.winner === null && g.turn === p;
          const ids = group ? groupBallIds(group) : [];
          const left = ids.filter((id) => !down.has(id)).length;
          const onEight = group !== null && left === 0;
          const sub =
            mode === "cpu" && p === 1
              ? `${DIFFICULTY_LABEL[difficulty]}${active && view.thinking ? " · thinking…" : ""}`
              : active && g.ballInHand !== "none"
                ? "ball in hand"
                : active
                  ? "at the table"
                  : "waiting";
          return (
            <section key={p} className={cx("bl-player", active && "is-active")} aria-current={active ? "true" : undefined}>
              <div className="bl-player-head">
                <span className="bl-turn-dot" aria-hidden />
                <span className="bl-player-name">{names[p]}</span>
                <span className={cx("bl-group", group && `is-${group}`)}>{group ?? "open"}</span>
              </div>
              <div className="bl-player-sub">{sub}</div>
              <div className="bl-player-balls" aria-label={group ? `${left} of ${group} left` : "Open table"}>
                {group ? (
                  onEight ? (
                    <>
                      <MiniBall id={8} />
                      <span className="bl-left">on the 8</span>
                    </>
                  ) : (
                    <>
                      {ids.map((id) => (
                        <MiniBall key={id} id={id} dim={down.has(id)} />
                      ))}
                      <span className="bl-left">{`${left} left`}</span>
                    </>
                  )
                ) : (
                  <span className="bl-left is-open">open table</span>
                )}
              </div>
            </section>
          );
        })}
      </div>
      <div className="bl-tray" aria-label="Pocketed balls">
        <span className="bl-tray-label">Pocketed</span>
        <div className="bl-tray-balls">
          {view.tray.length === 0 ? <span className="bl-tray-empty">—</span> : view.tray.map((id) => <MiniBall key={id} id={id} />)}
        </div>
      </div>
    </header>
  );
}
