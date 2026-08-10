/**
 * WarRoomLive.jsx — the live race view.
 *
 * Each participant gets their own editor and sees opponents' progress —
 * whether they are submitting and how it went — but never their code.
 * Updates arrive over a WebSocket fed by Redis, so a participant
 * connected to a different API instance still sees them.
 */

import { useState, useEffect, useRef, useCallback } from "react";
import { useParams, Link } from "react-router-dom";
import { useAuth } from "../context/AuthContext";
import NavBar from "../components/layout/NavBar";
import CodeEditor from "../components/editor/CodeEditor";
import LanguageSelector from "../components/editor/LanguageSelector";
import {
  fetchWarRoom,
  joinWarRoom,
  submitToWarRoom,
  openWarRoomSocket,
} from "../api/warrooms";
import { pollSubmission, isTerminalStatus } from "../api/submissions";
import { STARTER_CODE } from "../data/starterCode";
import "./WarRoom.css";

const STATUS_LABELS = {
  pending: "queued",
  running: "judging",
  accepted: "accepted",
  wrong_answer: "wrong answer",
  tle: "time limit",
  mle: "memory limit",
  runtime_error: "runtime error",
  compile_error: "compile error",
};

export default function WarRoomLive() {
  const { code } = useParams();
  const { user } = useAuth();

  const [room, setRoom] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [language, setLanguage] = useState("python");
  const [source, setSource] = useState(STARTER_CODE.python ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [myStatus, setMyStatus] = useState(null);

  // Opponent progress keyed by user id, updated from socket events.
  const [progress, setProgress] = useState({});
  const socketRef = useRef(null);

  const load = useCallback(async () => {
    try {
      // Joining is idempotent, so this also covers arriving by link.
      const joined = await joinWarRoom(code).catch(() => fetchWarRoom(code));
      setRoom(joined);
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not open this war room.");
    } finally {
      setLoading(false);
    }
  }, [code]);

  useEffect(() => { load(); }, [load]);

  // Keep a live socket open for as long as the view is mounted.
  useEffect(() => {
    if (!room) return undefined;

    const socket = openWarRoomSocket(code);
    socketRef.current = socket;

    socket.onmessage = (event) => {
      let parsed;
      try {
        parsed = JSON.parse(event.data);
      } catch {
        return;
      }

      const payload = parsed.payload ?? {};
      switch (parsed.type) {
        case "participant_joined":
        case "room_started":
          setRoom((current) => ({ ...(current ?? {}), ...payload }));
          break;
        case "submission_update":
          setProgress((current) => ({ ...current, [payload.userId]: payload }));
          break;
        case "room_finished":
          setRoom((current) => ({ ...(current ?? {}), ...payload }));
          break;
        default:
          break;
      }
    };

    socket.onerror = () => setError("Live updates disconnected. Refresh to reconnect.");

    return () => socket.close();
  }, [room?.id, code]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleLanguageChange = (next) => {
    setLanguage(next);
    setSource(STARTER_CODE[next] ?? "");
  };

  const handleSubmit = async () => {
    if (submitting) return;
    setSubmitting(true);
    setError(null);
    setMyStatus("pending");
    try {
      const queued = await submitToWarRoom(code, { language, code: source });
      if (queued.submissionId && !isTerminalStatus(queued.status)) {
        const final = await pollSubmission(queued.submissionId, {
          onUpdate: (s) => setMyStatus(s.status),
        });
        setMyStatus(final?.status ?? null);
      } else {
        setMyStatus(queued.status);
      }
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not submit.");
      setMyStatus(null);
    } finally {
      setSubmitting(false);
    }
  };

  if (loading) {
    return (
      <div className="wr-page">
        <NavBar />
        <div className="wr-loading"><div className="spinner spinner-lg" /></div>
      </div>
    );
  }

  if (!room) {
    return (
      <div className="wr-page">
        <NavBar />
        <div className="wr-empty-page">
          <p>{error ?? "War room not found."}</p>
          <Link to="/warrooms" className="btn btn-primary">Back to lobby</Link>
        </div>
      </div>
    );
  }

  const finished = room.status === "finished";
  const iWon = finished && room.winnerId === user?.id;
  const waiting = room.status === "waiting";

  return (
    <div className="wr-page">
      <NavBar />

      <div className="wr-live">
        {/* Status strip */}
        <div className="wr-strip">
          <div className="wr-strip-left">
            <span className="wr-room-code">{room.roomCode}</span>
            <span className="wr-strip-problem">{room.problemTitle}</span>
            <span className={`badge badge-${room.difficulty}`}>{room.difficulty}</span>
          </div>

          <div className="wr-players">
            {(room.participants ?? []).map((p) => {
              const isMe = p.userId === user?.id;
              const state = isMe ? myStatus : progress[p.userId]?.status;
              return (
                <div key={p.userId} className={`wr-player ${isMe ? "wr-player-me" : ""}`}>
                  <span className="wr-player-name">{p.username}{isMe && " (you)"}</span>
                  <span className={`wr-player-state state-${state ?? "idle"}`}>
                    {state ? STATUS_LABELS[state] ?? state : "thinking…"}
                  </span>
                </div>
              );
            })}
            {waiting &&
              Array.from({ length: room.maxParticipants - (room.participants?.length ?? 0) }).map((_, i) => (
                <div key={`empty-${i}`} className="wr-player wr-player-empty">
                  <span className="wr-player-name">waiting…</span>
                </div>
              ))}
          </div>
        </div>

        {finished && (
          <div className={`wr-result ${iWon ? "wr-result-win" : "wr-result-loss"}`}>
            {iWon ? "🏆 You won this race!" : `${room.winnerUsername} won this race.`}
            <Link to="/warrooms" className="wr-result-link">Back to lobby</Link>
          </div>
        )}

        {waiting && (
          <div className="wr-waiting">
            Waiting for another player. Share this code: <strong>{room.roomCode}</strong>
          </div>
        )}

        <div className="wr-body">
          <aside className="wr-statement">
            <h1 className="wr-statement-title">{room.problemTitle}</h1>
            <Link to={`/problems/${room.problemSlug}`} className="wr-statement-link" target="_blank">
              Open full problem ↗
            </Link>
          </aside>

          <div className="wr-editor">
            <div className="wr-toolbar">
              <LanguageSelector value={language} onChange={handleLanguageChange} disabled={submitting} />
              <button
                className="submit-btn"
                onClick={handleSubmit}
                disabled={submitting || waiting || finished}
                title={waiting ? "The race has not started yet" : undefined}
              >
                {submitting ? "Judging…" : "⬆ Submit"}
              </button>
            </div>
            <div className="wr-code-area">
              <CodeEditor language={language} value={source} onChange={setSource} />
            </div>
          </div>
        </div>

        {error && <p className="wr-error wr-error-inline">{error}</p>}
      </div>
    </div>
  );
}
