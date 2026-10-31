"use client";

import { useCallback, useRef } from "react";
import type { Voice } from "@/lib/match";

/**
 * Procedural Web Audio effects — no asset files, so nothing to load or 404.
 * The context is created lazily on the first sound because browsers refuse to
 * start one before a user gesture. Clicks and thuds are short bursts of
 * filtered noise over a pitched body, scaled by how hard the balls met, and
 * a break's flurry of contacts is thinned so it rattles instead of roaring.
 */
export function useSound(enabled: boolean) {
  const ctxRef = useRef<AudioContext | null>(null);
  const noiseRef = useRef<AudioBuffer | null>(null);
  const lastClick = useRef(0);

  const context = useCallback((): AudioContext | null => {
    if (typeof window === "undefined") return null;
    if (!ctxRef.current) {
      const Ctor =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
      if (!Ctor) return null;
      ctxRef.current = new Ctor();
    }
    if (ctxRef.current.state === "suspended") void ctxRef.current.resume();
    return ctxRef.current;
  }, []);

  /** Half a second of white noise, made once and reused by every burst. */
  const noise = useCallback((ctx: AudioContext): AudioBuffer => {
    if (!noiseRef.current || noiseRef.current.sampleRate !== ctx.sampleRate) {
      const buf = ctx.createBuffer(1, Math.floor(ctx.sampleRate * 0.5), ctx.sampleRate);
      const data = buf.getChannelData(0);
      let seed = 1;
      for (let i = 0; i < data.length; i++) {
        seed = (seed * 16807) % 2147483647;
        data[i] = (seed / 2147483647) * 2 - 1;
      }
      noiseRef.current = buf;
    }
    return noiseRef.current;
  }, []);

  const tone = useCallback(
    (ctx: AudioContext, freq: number, dur: number, type: OscillatorType, gain: number, slideTo?: number, delay = 0) => {
      const t = ctx.currentTime + delay;
      const osc = ctx.createOscillator();
      const amp = ctx.createGain();
      osc.type = type;
      osc.frequency.setValueAtTime(freq, t);
      if (slideTo !== undefined) osc.frequency.exponentialRampToValueAtTime(Math.max(1, slideTo), t + dur);
      amp.gain.setValueAtTime(Math.max(0.0001, gain), t);
      amp.gain.exponentialRampToValueAtTime(0.0001, t + dur);
      osc.connect(amp).connect(ctx.destination);
      osc.start(t);
      osc.stop(t + dur + 0.02);
      // Release the nodes as soon as they are silent so nothing accumulates.
      osc.onended = () => {
        osc.disconnect();
        amp.disconnect();
      };
    },
    []
  );

  const burst = useCallback(
    (ctx: AudioContext, freq: number, q: number, dur: number, gain: number, type: BiquadFilterType = "bandpass", delay = 0) => {
      const t = ctx.currentTime + delay;
      const src = ctx.createBufferSource();
      src.buffer = noise(ctx);
      const filter = ctx.createBiquadFilter();
      filter.type = type;
      filter.frequency.value = freq;
      filter.Q.value = q;
      const amp = ctx.createGain();
      amp.gain.setValueAtTime(Math.max(0.0001, gain), t);
      amp.gain.exponentialRampToValueAtTime(0.0001, t + dur);
      src.connect(filter).connect(amp).connect(ctx.destination);
      src.start(t, Math.random() * 0.3);
      src.stop(t + dur + 0.02);
      src.onended = () => {
        src.disconnect();
        filter.disconnect();
        amp.disconnect();
      };
    },
    [noise]
  );

  const play = useCallback(
    (voice: Voice, magnitude = 1) => {
      if (!enabled) return;
      const ctx = context();
      if (!ctx) return;
      const m = Math.min(1, Math.max(0, magnitude));

      switch (voice) {
        case "cue":
          // Leather tip on phenolic: a sharp tock with a woody body.
          burst(ctx, 2600, 1.4, 0.035, 0.12 + 0.3 * m);
          tone(ctx, 720, 0.06, "triangle", 0.05 + 0.12 * m, 260);
          break;
        case "click": {
          // Ball on ball: bright, short, louder and higher the harder it is.
          const now = ctx.currentTime;
          if (now - lastClick.current < 0.012) return;
          lastClick.current = now;
          const g = 0.02 + 0.32 * m * m;
          burst(ctx, 3200 + 1600 * m, 3.5, 0.03 + 0.03 * m, g);
          tone(ctx, 2100 + 900 * m, 0.045, "sine", g * 0.35);
          break;
        }
        case "cushion":
          // Rubber and wood: a low, dull thump.
          tone(ctx, 135, 0.12, "sine", 0.03 + 0.16 * m, 70);
          burst(ctx, 380, 1.2, 0.07, 0.02 + 0.08 * m, "lowpass");
          break;
        case "pocket":
          // Down the hole: a hollow clunk, then a rattle along the return.
          tone(ctx, 190, 0.2, "sine", 0.16, 85);
          burst(ctx, 900, 2, 0.08, 0.07);
          burst(ctx, 1500, 3, 0.04, 0.05, "bandpass", 0.09);
          burst(ctx, 1300, 3, 0.04, 0.035, "bandpass", 0.17);
          break;
        case "place":
          tone(ctx, 520, 0.06, "sine", 0.035 * (0.5 + m));
          break;
        case "turn":
          tone(ctx, 660, 0.08, "triangle", 0.03);
          tone(ctx, 880, 0.1, "triangle", 0.025, undefined, 0.07);
          break;
        case "group":
          [660, 880, 1046].forEach((f, i) => tone(ctx, f, 0.14, "triangle", 0.04, undefined, i * 0.07));
          break;
        case "foul":
          tone(ctx, 220, 0.26, "sawtooth", 0.05, 110);
          tone(ctx, 196, 0.26, "square", 0.02, 98, 0.03);
          break;
        case "win":
          [523, 659, 784, 1046, 1318].forEach((f, i) => tone(ctx, f, 0.26, "triangle", 0.055, undefined, i * 0.1));
          break;
        case "lose":
          [392, 330, 262, 196].forEach((f, i) => tone(ctx, f, 0.3, "triangle", 0.05, undefined, i * 0.13));
          break;
      }
    },
    [burst, context, enabled, tone]
  );

  return play;
}
