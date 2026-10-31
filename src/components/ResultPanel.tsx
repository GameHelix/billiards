"use client";

import type { TierRecord } from "./BilliardsGame";
import { DIFFICULTY_LABEL } from "@/lib/game/constants";
import type { PlayerStats } from "@/lib/game/rules";
import type { Difficulty, EndReason, Mode, PlayerIndex } from "@/lib/game/types";

export interface MatchResult {
  winner: PlayerIndex;
  reason: EndReason;
  names: [string, string];
  mode: Mode;
  difficulty: Difficulty;
  stats: [PlayerStats, PlayerStats];
  /** Record against this CPU tier after the game, when playing the CPU. */
  record: TierRecord | null;
}

interface ResultPanelProps {
  result: MatchResult;
  onPlayAgain: () => void;
  onMenu: () => void;
}

function headline(r: MatchResult): string {
  if (r.mode === "cpu") return r.winner === 0 ? "🏆 You win!" : "💀 CPU wins";
  return `🏆 ${r.names[r.winner]} wins!`;
}

function reasonText(r: MatchResult): string {
  const winner = r.names[r.winner];
  const loser = r.names[r.winner === 0 ? 1 : 0];
  const you = r.mode === "cpu";
  switch (r.reason) {
    case "eight-legal":
      return you && r.winner === 0 ? "You sank the 8-ball to close it out." : `${winner} sank the 8-ball to close it out.`;
    case "eight-early":
      return you && r.winner === 1 ? "You sank the 8 before clearing your group." : `${loser} sank the 8 too early.`;
    case "eight-foul":
      return you && r.winner === 1 ? "You fouled on the shot that dropped the 8." : `${loser} fouled while sinking the 8.`;
  }
}

/** End-of-game summary: who won and how, the shooting numbers, the record. */
export function ResultPanel({ result, onPlayAgain, onMenu }: ResultPanelProps) {
  const good = result.mode === "local" || result.winner === 0;
  return (
    <div className="bl-overlay" role="dialog" aria-modal aria-label="Game over">
      <div className="bl-dialog">
        <h2 className="bl-dialog-title">Game over</h2>
        <p className={good ? "bl-result is-win" : "bl-result is-loss"}>{headline(result)}</p>
        <p className="bl-result-note">{reasonText(result)}</p>

        <table className="bl-score">
          <thead>
            <tr>
              <th scope="col" />
              <th scope="col">{result.names[0]}</th>
              <th scope="col">{result.names[1]}</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th scope="row">Shots</th>
              <td>{result.stats[0].shots}</td>
              <td>{result.stats[1].shots}</td>
            </tr>
            <tr>
              <th scope="row">Pocketed</th>
              <td>{result.stats[0].potted}</td>
              <td>{result.stats[1].potted}</td>
            </tr>
            <tr>
              <th scope="row">Fouls</th>
              <td>{result.stats[0].fouls}</td>
              <td>{result.stats[1].fouls}</td>
            </tr>
          </tbody>
        </table>

        {result.record ? (
          <p className="bl-result-note">
            {`Record vs ${DIFFICULTY_LABEL[result.difficulty]} CPU: `}
            <b>{`${result.record.wins} won`}</b>
            {` · ${result.record.losses} lost`}
          </p>
        ) : null}

        <button type="button" className="bl-primary" onClick={onPlayAgain} autoFocus>
          Play again
        </button>
        <button type="button" className="bl-ghost" onClick={onMenu}>
          Back to menu
        </button>
      </div>
    </div>
  );
}
