import api from "./axios";

/** Open a new room. Omit problemSlug to race on a random problem. */
export async function createWarRoom({ problemSlug, maxParticipants } = {}) {
  const { data } = await api.post("/warrooms", { problemSlug, maxParticipants });
  return data.data;
}

/** List rooms still waiting for players. */
export async function fetchOpenWarRooms() {
  const { data } = await api.get("/warrooms");
  return data.data ?? [];
}

/** List rooms the signed-in user has taken part in. */
export async function fetchMyWarRooms() {
  const { data } = await api.get("/warrooms/mine");
  return data.data ?? [];
}

/** Fetch one room by its shareable code. */
export async function fetchWarRoom(code) {
  const { data } = await api.get(`/warrooms/${code}`);
  return data.data;
}

/** Join a room. Rejoining a room you are already in is harmless. */
export async function joinWarRoom(code) {
  const { data } = await api.post(`/warrooms/${code}/join`);
  return data.data;
}

/** Submit a solution inside a race; routed to the priority judge lane. */
export async function submitToWarRoom(code, { language, code: source }) {
  const { data } = await api.post(`/warrooms/${code}/submit`, { language, code: source });
  return data;
}

/**
 * Open the live race WebSocket.
 *
 * The auth cookie rides along with the handshake, so no token handling
 * is needed here either. The caller owns the returned socket and should
 * close it when the view unmounts.
 */
export function openWarRoomSocket(code) {
  const httpBase = api.defaults.baseURL.replace(/\/api\/?$/, "");
  const wsBase = httpBase.replace(/^http/, "ws");
  return new WebSocket(`${wsBase}/ws/warroom/${code}`);
}
