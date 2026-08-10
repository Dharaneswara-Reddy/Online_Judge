/**
 * WarRoomLobby.jsx — find or start a race.
 *
 * Lists rooms still waiting for players, creates new ones, and takes a
 * room code so a room can be shared with a friend directly.
 */

import { useState, useEffect, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import NavBar from "../components/layout/NavBar";
import {
  fetchOpenWarRooms,
  fetchMyWarRooms,
  createWarRoom,
  joinWarRoom,
} from "../api/warrooms";
import "./WarRoom.css";

export default function WarRoomLobby() {
  const navigate = useNavigate();
  const [rooms, setRooms] = useState([]);
  const [myRooms, setMyRooms] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const [size, setSize] = useState(2);
  const [joinCode, setJoinCode] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [open, mine] = await Promise.all([fetchOpenWarRooms(), fetchMyWarRooms()]);
      setRooms(open);
      setMyRooms(mine);
    } catch {
      setError("Could not load war rooms.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleCreate = async () => {
    setBusy(true);
    setError(null);
    try {
      const room = await createWarRoom({ maxParticipants: size });
      navigate(`/warrooms/${room.roomCode}`);
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not create a room.");
      setBusy(false);
    }
  };

  const handleJoin = async (code) => {
    if (!code) return;
    setBusy(true);
    setError(null);
    try {
      await joinWarRoom(code.toUpperCase());
      navigate(`/warrooms/${code.toUpperCase()}`);
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not join that room.");
      setBusy(false);
    }
  };

  return (
    <div className="wr-page">
      <NavBar />

      <main className="wr-main">
        <header className="wr-header">
          <h1 className="wr-title">War Rooms</h1>
          <p className="wr-subtitle">
            Race another coder on the same problem. First accepted verdict wins — no
            scheduling, no contest to wait for.
          </p>
        </header>

        {error && <p className="wr-error">{error}</p>}

        <section className="wr-actions">
          <div className="card wr-action-card">
            <h2 className="wr-card-title">Start a race</h2>
            <p className="wr-card-text">A random problem is picked for everyone in the room.</p>
            <div className="wr-size-picker">
              {[2, 3].map((n) => (
                <button
                  key={n}
                  className={`wr-size ${size === n ? "wr-size-active" : ""}`}
                  onClick={() => setSize(n)}
                >
                  {n} players
                </button>
              ))}
            </div>
            <button className="btn btn-primary" onClick={handleCreate} disabled={busy}>
              {busy ? "Creating…" : "Create room"}
            </button>
          </div>

          <div className="card wr-action-card">
            <h2 className="wr-card-title">Join with a code</h2>
            <p className="wr-card-text">Got a room code from a friend? Enter it here.</p>
            <input
              className="form-input wr-code-input"
              placeholder="ABC123"
              value={joinCode}
              onChange={(e) => setJoinCode(e.target.value.toUpperCase())}
              maxLength={6}
            />
            <button
              className="btn btn-primary"
              onClick={() => handleJoin(joinCode)}
              disabled={busy || joinCode.length !== 6}
            >
              Join
            </button>
          </div>
        </section>

        <section>
          <h2 className="wr-section-title">Open rooms</h2>
          {loading ? (
            <div className="wr-loading"><div className="spinner" /></div>
          ) : rooms.length === 0 ? (
            <p className="wr-empty">No one is waiting right now. Create a room and share the code.</p>
          ) : (
            <ul className="wr-room-list">
              {rooms.map((room) => (
                <li key={room.id} className="wr-room">
                  <div className="wr-room-info">
                    <span className="wr-room-code">{room.roomCode}</span>
                    <span className="wr-room-problem">{room.problemTitle}</span>
                    <span className={`badge badge-${room.difficulty}`}>{room.difficulty}</span>
                  </div>
                  <div className="wr-room-right">
                    <span className="wr-room-slots">
                      {room.participants?.length ?? 0} / {room.maxParticipants}
                    </span>
                    <button
                      className="btn btn-primary wr-join-btn"
                      onClick={() => handleJoin(room.roomCode)}
                      disabled={busy}
                    >
                      Join
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        {myRooms.length > 0 && (
          <section>
            <h2 className="wr-section-title">Your recent races</h2>
            <ul className="wr-room-list">
              {myRooms.slice(0, 8).map((room) => (
                <li key={room.id} className="wr-room">
                  <div className="wr-room-info">
                    <span className="wr-room-code">{room.roomCode}</span>
                    <span className="wr-room-problem">{room.problemTitle}</span>
                  </div>
                  <div className="wr-room-right">
                    <span className={`wr-status wr-status-${room.status}`}>
                      {room.status === "finished" && room.winnerUsername
                        ? `won by ${room.winnerUsername}`
                        : room.status.replace("_", " ")}
                    </span>
                    <button
                      className="btn btn-ghost wr-join-btn"
                      onClick={() => navigate(`/warrooms/${room.roomCode}`)}
                    >
                      View
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          </section>
        )}
      </main>
    </div>
  );
}
