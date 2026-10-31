"use client";

import { useCallback, useState } from "react";
import { MenuScreen } from "./MenuScreen";
import { PlayScreen } from "./PlayScreen";
import { ResultPanel, type MatchResult } from "./ResultPanel";
import { RulesPanel } from "./RulesPanel";
import { cx } from "@/lib/cx";
import { PREFS_KEY, STATS_KEY } from "@/lib/game/constants";
import { randomSeed } from "@/lib/game/rng";
import type { GameState } from "@/lib/game/rules";
import type { Difficulty, Mode, PlayerIndex } from "@/lib/game/types";
import { useLocalStorage } from "@/lib/hooks/useLocalStorage";
import { useSound } from "@/lib/hooks/useSound";

export interface Prefs {
  mode: Mode;
  difficulty: Difficulty;
  sound: boolean;
}

export interface TierRecord {
  wins: number;
  losses: number;
}

/** Wins and losses against each CPU tier, plus two-player games played. */
export interface Stats {
  easy: TierRecord;
  medium: TierRecord;
  hard: TierRecord;
  duels: number;
}

const DEFAULT_PREFS: Prefs = { mode: "cpu", difficulty: "medium", sound: true };
const EMPTY_STATS: Stats = {
  easy: { wins: 0, losses: 0 },
  medium: { wins: 0, losses: 0 },
  hard: { wins: 0, losses: 0 },
  duels: 0,
};

interface Session {
  seed: number;
  breaker: PlayerIndex;
  mode: Mode;
  difficulty: Difficulty;
  /** Bumped for every game so the play screen remounts clean. */
  nonce: number;
}

export function namesFor(mode: Mode): [string, string] {
  return mode === "cpu" ? ["You", "CPU"] : ["Player 1", "Player 2"];
}

/**
 * Top-level orchestrator: routes between the menu and a match, and owns the
 * persisted choices and record, the sound switch, the rules dialog and the
 * result panel. The rack's seed is drawn in the Start click, so nothing
 * random runs during server rendering and the first paint matches it.
 */
export function BilliardsGame() {
  const { value: prefs, store: storePrefs } = useLocalStorage<Prefs>(PREFS_KEY, DEFAULT_PREFS);
  const { value: stats, store: storeStats } = useLocalStorage<Stats>(STATS_KEY, EMPTY_STATS);
  const [session, setSession] = useState<Session | null>(null);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [result, setResult] = useState<MatchResult | null>(null);
  const play = useSound(prefs.sound);

  const begin = useCallback(
    (breaker: PlayerIndex) => {
      const seed = randomSeed();
      setResult(null);
      setSession((prev) => ({
        seed,
        breaker,
        mode: prefs.mode,
        difficulty: prefs.difficulty,
        nonce: (prev?.nonce ?? 0) + 1,
      }));
    },
    [prefs.mode, prefs.difficulty]
  );

  const handleStart = useCallback(() => begin(0), [begin]);

  // A rematch hands the break to the other side.
  const handleRematch = useCallback(() => begin(session?.breaker === 0 ? 1 : 0), [begin, session?.breaker]);

  const handleMenu = useCallback(() => {
    setResult(null);
    setSession(null);
  }, []);

  const handleGameOver = useCallback(
    (game: GameState) => {
      if (!session || game.winner === null || game.endReason === null) return;
      let record: TierRecord | null = null;
      if (session.mode === "cpu") {
        const prev = stats[session.difficulty] ?? { wins: 0, losses: 0 };
        record = game.winner === 0 ? { ...prev, wins: prev.wins + 1 } : { ...prev, losses: prev.losses + 1 };
        storeStats({ ...EMPTY_STATS, ...stats, [session.difficulty]: record });
      } else {
        storeStats({ ...EMPTY_STATS, ...stats, duels: (stats.duels ?? 0) + 1 });
      }
      setResult({
        winner: game.winner,
        reason: game.endReason,
        names: namesFor(session.mode),
        mode: session.mode,
        difficulty: session.difficulty,
        stats: game.stats,
        record,
      });
    },
    [session, stats, storeStats]
  );

  const toggleSound = useCallback(() => storePrefs({ ...prefs, sound: !prefs.sound }), [prefs, storePrefs]);

  return (
    <main className={cx("bl-app", session && "is-play")}>
      {session ? (
        <PlayScreen
          key={session.nonce}
          seed={session.seed}
          breaker={session.breaker}
          mode={session.mode}
          difficulty={session.difficulty}
          names={namesFor(session.mode)}
          soundOn={prefs.sound}
          play={play}
          paused={rulesOpen}
          onGameOver={handleGameOver}
          onNewGame={handleRematch}
          onMenu={handleMenu}
          onRules={() => setRulesOpen(true)}
          onToggleSound={toggleSound}
        />
      ) : (
        <MenuScreen
          prefs={prefs}
          stats={stats}
          onPrefs={storePrefs}
          onStart={handleStart}
          onRules={() => setRulesOpen(true)}
        />
      )}

      {result ? <ResultPanel result={result} onPlayAgain={handleRematch} onMenu={handleMenu} /> : null}
      {rulesOpen ? <RulesPanel onClose={() => setRulesOpen(false)} /> : null}
    </main>
  );
}
