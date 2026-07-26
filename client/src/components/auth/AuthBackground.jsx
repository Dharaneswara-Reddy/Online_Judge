/**
 * AuthBackground.jsx — Mouse-reactive dot-field background
 *
 * Renders a full-viewport canvas filled with a grid of small
 * dots. When the user moves their mouse, nearby dots push away
 * and glow with the --code-accent teal color (#6fb3a8).
 *
 * Performance notes:
 * - Uses requestAnimationFrame for smooth 60fps rendering
 * - Respects prefers-reduced-motion: renders a single static
 *   frame with no animation loop and no mouse reactivity
 * - Canvas uses devicePixelRatio for crisp rendering on HiDPI
 * - Cleanup on unmount cancels RAF and removes event listeners
 *
 * The canvas sits behind the auth card (z-index: 0) and does
 * not intercept pointer events.
 */

import { useEffect, useRef } from 'react';
import './AuthBackground.css';

// Grid spacing between dots (px)
const SPACING = 34;

// Mouse influence radius (px) — dots within this distance react
const RADIUS = 130;

// Max displacement when a dot is pushed by the cursor (px)
const MAX_PUSH = 12;

// Base dot color (muted gray, matches --text-muted range)
const BASE = { r: 70, g: 72, b: 80 };

// Glow dot color (teal, matches --code-accent #6fb3a8)
const GLOW = { r: 111, g: 179, b: 168 };

function AuthBackground() {
  const wrapRef = useRef(null);
  const canvasRef = useRef(null);

  useEffect(() => {
    const wrap = wrapRef.current;
    const canvas = canvasRef.current;
    if (!wrap || !canvas) return;

    // Check if user prefers reduced motion
    const reduceMotion = window.matchMedia(
      '(prefers-reduced-motion: reduce)'
    ).matches;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    let dots = [];
    let mouse = { x: -9999, y: -9999 };
    let raf = 0;

    /**
     * build — (re)creates the dot grid based on current
     * viewport dimensions. Called on mount and window resize.
     */
    function build() {
      const w = wrap.clientWidth;
      const h = wrap.clientHeight;
      const dpr = window.devicePixelRatio || 1;

      // Size the canvas to match the wrapper at native resolution
      canvas.width = w * dpr;
      canvas.height = h * dpr;
      canvas.style.width = `${w}px`;
      canvas.style.height = `${h}px`;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

      // Build the dot grid
      dots = [];
      for (let y = SPACING / 2; y < h; y += SPACING) {
        for (let x = SPACING / 2; x < w; x += SPACING) {
          dots.push({ bx: x, by: y, x, y });
        }
      }
    }

    /**
     * frame — renders one animation frame. For each dot:
     * 1. Calculate distance from mouse cursor
     * 2. If within RADIUS, push the dot away and add glow
     * 3. Smooth the position with linear interpolation (lerp)
     * 4. Draw the dot with interpolated color and opacity
     */
    function frame() {
      const w = wrap.clientWidth;
      const h = wrap.clientHeight;
      ctx.clearRect(0, 0, w, h);

      for (const d of dots) {
        const dx = mouse.x - d.bx;
        const dy = mouse.y - d.by;
        const dist = Math.sqrt(dx * dx + dy * dy);
        let tx = d.bx;
        let ty = d.by;
        let glow = 0;

        if (dist < RADIUS) {
          // Falloff factor: 1 at center, 0 at edge of radius
          const f = 1 - dist / RADIUS;
          const ang = Math.atan2(dy, dx);

          // Push the dot away from the cursor
          tx = d.bx - Math.cos(ang) * f * MAX_PUSH;
          ty = d.by - Math.sin(ang) * f * MAX_PUSH;
          glow = f;
        }

        // Lerp current position toward target (spring-like easing)
        d.x += (tx - d.x) * 0.12;
        d.y += (ty - d.y) * 0.12;

        // Interpolate color from BASE gray to GLOW teal
        const r = BASE.r + (GLOW.r - BASE.r) * glow;
        const g = BASE.g + (GLOW.g - BASE.g) * glow;
        const b = BASE.b + (GLOW.b - BASE.b) * glow;
        const a = 0.3 + glow * 0.6;

        ctx.beginPath();
        ctx.arc(d.x, d.y, 1.4, 0, Math.PI * 2);
        ctx.fillStyle = `rgba(${r},${g},${b},${a})`;
        ctx.fill();
      }

      raf = requestAnimationFrame(frame);
    }

    function handleMove(e) {
      const rect = wrap.getBoundingClientRect();
      mouse.x = e.clientX - rect.left;
      mouse.y = e.clientY - rect.top;
    }

    function handleLeave() {
      mouse.x = -9999;
      mouse.y = -9999;
    }

    // Initial build
    build();
    window.addEventListener('resize', build);

    if (!reduceMotion) {
      // Full interactive mode: mouse tracking + animation loop
      wrap.addEventListener('mousemove', handleMove);
      wrap.addEventListener('mouseleave', handleLeave);
      raf = requestAnimationFrame(frame);
    } else {
      // Reduced motion: static single render, no loop
      frame();
    }

    // Cleanup on unmount
    return () => {
      window.removeEventListener('resize', build);
      wrap.removeEventListener('mousemove', handleMove);
      wrap.removeEventListener('mouseleave', handleLeave);
      cancelAnimationFrame(raf);
    };
  }, []);

  return (
    <div ref={wrapRef} className="auth-bg-wrap">
      <canvas ref={canvasRef} className="auth-bg-canvas" />
    </div>
  );
}

export default AuthBackground;
