/**
 * DiscussionPanel.jsx — the per-problem comment thread.
 *
 * Threads are one level deep by design, so this renders a flat list of
 * comments each with its own replies; there is no recursion to worry
 * about.
 */

import { useState, useEffect, useCallback } from "react";
import {
  fetchDiscussions,
  postComment,
  setUpvote,
  deleteComment,
} from "../../api/discussions";
import "./DiscussionPanel.css";

export default function DiscussionPanel({ slug, currentUser }) {
  const [threads, setThreads] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const [draft, setDraft] = useState("");
  const [posting, setPosting] = useState(false);
  const [replyTo, setReplyTo] = useState(null);
  const [replyDraft, setReplyDraft] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setThreads(await fetchDiscussions(slug));
    } catch {
      setError("Could not load the discussion.");
    } finally {
      setLoading(false);
    }
  }, [slug]);

  useEffect(() => { load(); }, [load]);

  const handlePost = async (e) => {
    e.preventDefault();
    if (!draft.trim() || posting) return;
    setPosting(true);
    setError(null);
    try {
      await postComment(slug, { content: draft });
      setDraft("");
      await load();
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not post your comment.");
    } finally {
      setPosting(false);
    }
  };

  const handleReply = async (parentId) => {
    if (!replyDraft.trim() || posting) return;
    setPosting(true);
    setError(null);
    try {
      await postComment(slug, { content: replyDraft, parentId });
      setReplyDraft("");
      setReplyTo(null);
      await load();
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not post your reply.");
    } finally {
      setPosting(false);
    }
  };

  // Votes update the row in place: a full reload would scroll the reader
  // away from the comment they just voted on.
  const handleVote = async (comment) => {
    try {
      const result = await setUpvote(comment.id, !comment.upvotedByMe);
      setThreads((current) =>
        current.map((thread) => ({
          ...thread,
          ...(thread.id === comment.id ? result : {}),
          replies: (thread.replies ?? []).map((reply) =>
            reply.id === comment.id ? { ...reply, ...result } : reply
          ),
        }))
      );
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not record your vote.");
    }
  };

  const handleDelete = async (comment) => {
    try {
      await deleteComment(comment.id);
      await load();
    } catch (err) {
      setError(err?.response?.data?.message ?? "Could not delete that comment.");
    }
  };

  const canModerate = (comment) =>
    currentUser && (comment.userId === currentUser.id || currentUser.role === "admin");

  if (loading) {
    return <div className="discussion-loading"><div className="spinner" /></div>;
  }

  return (
    <div className="discussion-panel">
      {currentUser ? (
        <form onSubmit={handlePost} className="discussion-composer">
          <textarea
            className="form-input discussion-input"
            placeholder="Ask a question or share your approach…"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={3}
            maxLength={4000}
          />
          <div className="discussion-composer-actions">
            <span className="discussion-count">{draft.length}/4000</span>
            <button type="submit" className="btn btn-primary" disabled={posting || !draft.trim()}>
              {posting ? "Posting…" : "Post"}
            </button>
          </div>
        </form>
      ) : (
        <p className="discussion-signin">Sign in to join the discussion.</p>
      )}

      {error && <p className="discussion-error">{error}</p>}

      {threads.length === 0 ? (
        <p className="discussion-empty">No comments yet. Be the first to explain your approach.</p>
      ) : (
        <ul className="discussion-list">
          {threads.map((thread) => (
            <li key={thread.id} className="discussion-thread">
              <Comment
                comment={thread}
                canModerate={canModerate(thread)}
                canVote={Boolean(currentUser)}
                onVote={handleVote}
                onDelete={handleDelete}
                onReply={currentUser ? () => setReplyTo(replyTo === thread.id ? null : thread.id) : null}
              />

              {replyTo === thread.id && (
                <div className="discussion-reply-box">
                  <textarea
                    className="form-input discussion-input"
                    placeholder="Write a reply…"
                    value={replyDraft}
                    onChange={(e) => setReplyDraft(e.target.value)}
                    rows={2}
                    maxLength={4000}
                  />
                  <div className="discussion-composer-actions">
                    <button
                      className="btn btn-primary"
                      onClick={() => handleReply(thread.id)}
                      disabled={posting || !replyDraft.trim()}
                    >
                      Reply
                    </button>
                    <button className="btn btn-ghost" onClick={() => { setReplyTo(null); setReplyDraft(""); }}>
                      Cancel
                    </button>
                  </div>
                </div>
              )}

              {(thread.replies ?? []).length > 0 && (
                <ul className="discussion-replies">
                  {thread.replies.map((reply) => (
                    <li key={reply.id}>
                      <Comment
                        comment={reply}
                        canModerate={canModerate(reply)}
                        canVote={Boolean(currentUser)}
                        onVote={handleVote}
                        onDelete={handleDelete}
                      />
                    </li>
                  ))}
                </ul>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Comment({ comment, canModerate, canVote, onVote, onDelete, onReply }) {
  if (comment.deleted) {
    return <div className="discussion-comment discussion-deleted">[comment removed]</div>;
  }

  return (
    <div className="discussion-comment">
      <div className="discussion-comment-head">
        <span className="discussion-author">{comment.username}</span>
        <span className="discussion-time">{new Date(comment.createdAt).toLocaleString()}</span>
      </div>

      <p className="discussion-content">{comment.content}</p>

      <div className="discussion-actions">
        <button
          className={`discussion-vote ${comment.upvotedByMe ? "discussion-voted" : ""}`}
          onClick={() => onVote(comment)}
          disabled={!canVote}
          title={canVote ? "Upvote" : "Sign in to vote"}
        >
          ▲ {comment.upvotes}
        </button>
        {onReply && (
          <button className="discussion-link-btn" onClick={onReply}>Reply</button>
        )}
        {canModerate && (
          <button className="discussion-link-btn discussion-danger" onClick={() => onDelete(comment)}>
            Delete
          </button>
        )}
      </div>
    </div>
  );
}
