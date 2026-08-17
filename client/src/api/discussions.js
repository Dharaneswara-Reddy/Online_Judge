import api from "./axios";

/**
 * Fetch one page of a problem's discussion.
 *
 * Threads are returned newest first with their replies nested. Pass the
 * `nextCursor` from a previous response to fetch the following page; the
 * cursor is opaque and should only ever be handed back unchanged.
 */
export async function fetchDiscussions(slug, { cursor, limit } = {}) {
  const params = {};
  if (cursor) params.cursor = cursor;
  if (limit) params.limit = limit;

  const { data } = await api.get(`/problems/${slug}/discussions`, { params });
  return {
    threads: data.data ?? [],
    nextCursor: data.nextCursor ?? null,
    hasMore: Boolean(data.hasMore),
  };
}

/** Post a comment, or a reply when parentId is supplied. */
export async function postComment(slug, { content, parentId } = {}) {
  const { data } = await api.post(`/problems/${slug}/discussions`, { content, parentId });
  return data.data;
}

/**
 * Toggle the signed-in user's upvote on a comment.
 * Returns the new count so the caller can update in place.
 */
export async function setUpvote(commentId, upvoted) {
  const { data } = upvoted
    ? await api.post(`/discussions/${commentId}/upvote`)
    : await api.delete(`/discussions/${commentId}/upvote`);
  return data.data;
}

/** Delete a comment. Users may delete their own; admins may delete any. */
export async function deleteComment(commentId) {
  await api.delete(`/discussions/${commentId}`);
}
