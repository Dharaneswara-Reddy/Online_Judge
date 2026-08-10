import api from "./axios";

/** Fetch a problem's discussion as threads with nested replies. */
export async function fetchDiscussions(slug) {
  const { data } = await api.get(`/problems/${slug}/discussions`);
  return data.data ?? [];
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
