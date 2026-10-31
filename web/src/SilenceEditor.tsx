import { useEffect, useRef, useState } from "react";
import { api } from "./api";
import type { Alert, Silence } from "./model";
function localTime(iso: string) {
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
}
export default function SilenceEditor({
  initial,
  onClose,
  onSaved,
}: {
  initial: Silence;
  onClose: () => void;
  onSaved: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [value, setValue] = useState(initial),
    [start, setStart] = useState(localTime(initial.startsAt)),
    [end, setEnd] = useState(localTime(initial.endsAt));
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [preview, setPreview] = useState<{ count: number; alerts: Alert[] } | null>(
      null,
    ),
    [previewError, setPreviewError] = useState(""),
    [confirmed, setConfirmed] = useState(false);
  useEffect(() => {
    ref.current?.showModal();
  }, []);
  useEffect(() => {
    let ignore = false;
    setPreview(null);
    setPreviewError("");
    setConfirmed(false);
    const timer = setTimeout(() => {
      void api<{ count: number; alerts: Alert[] }>("silences/preview", {
        method: "POST",
        body: JSON.stringify({ matchers: value.matchers }),
      })
        .then((p) => {
          if (!ignore) setPreview(p);
        })
        .catch((e) => {
          if (!ignore) setPreviewError(e.message);
        });
    }, 350);
    return () => {
      ignore = true;
      clearTimeout(timer);
    };
  }, [value.matchers]);
  return (
    <dialog ref={ref} className="editor" onCancel={onClose}>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setError("");
          if (!confirmed || !preview) return;
          if (Date.parse(end) <= Date.parse(start)) {
            setError("End time must be after start time.");
            return;
          }
          setBusy(true);
          try {
            await api("silences", {
              method: "POST",
              body: JSON.stringify({
                id: value.id,
                matchers: value.matchers,
                createdBy: value.createdBy,
                comment: value.comment,
                startsAt: new Date(start).toISOString(),
                endsAt: new Date(end).toISOString(),
              }),
            });
            onSaved();
          } catch (err) {
            setError((err as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="dialog-head">
          <div>
            <div className="eyebrow">NOTIFICATION CONTROL</div>
            <h2>{initial.id ? "Edit silence" : "New silence"}</h2>
          </div>
          <button type="button" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
        <fieldset>
          <legend>Match alerts</legend>
          {value.matchers.map((m, i) => (
            <div className="matcher-row" key={i}>
              <input
                aria-label={`Matcher ${i + 1} label`}
                placeholder="Label"
                required
                value={m.name}
                onChange={(e) =>
                  setValue({
                    ...value,
                    matchers: value.matchers.map((x, j) =>
                      j === i ? { ...x, name: e.target.value } : x,
                    ),
                  })
                }
              />
              <select
                aria-label={`Matcher ${i + 1} operator`}
                value={
                  (m.isEqual === false ? "!" : "=") +
                  (m.isRegex ? "~" : m.isEqual === false ? "=" : "")
                }
                onChange={(e) =>
                  setValue({
                    ...value,
                    matchers: value.matchers.map((x, j) =>
                      j === i
                        ? {
                            ...x,
                            isRegex: e.target.value.endsWith("~"),
                            isEqual: !e.target.value.startsWith("!"),
                          }
                        : x,
                    ),
                  })
                }
              >
                <option value="=">=</option>
                <option value="!=">!=</option>
                <option value="=~">=~</option>
                <option value="!~">!~</option>
              </select>
              <input
                aria-label={`Matcher ${i + 1} value`}
                placeholder="Value"
                value={m.value}
                onChange={(e) =>
                  setValue({
                    ...value,
                    matchers: value.matchers.map((x, j) =>
                      j === i ? { ...x, value: e.target.value } : x,
                    ),
                  })
                }
              />
              <button
                type="button"
                aria-label={`Remove matcher ${i + 1}`}
                disabled={value.matchers.length === 1}
                onClick={() =>
                  setValue({
                    ...value,
                    matchers: value.matchers.filter((_, j) => i !== j),
                  })
                }
              >
                ×
              </button>
            </div>
          ))}
          <button
            type="button"
            className="text-button"
            onClick={() =>
              setValue({
                ...value,
                matchers: [
                  ...value.matchers,
                  { name: "", value: "", isEqual: true, isRegex: false },
                ],
              })
            }
          >
            ＋ Add matcher
          </button>
        </fieldset>
        <div className="preview-matches" aria-live="polite">
          {previewError ? (
            <span className="critical-text">{previewError}</span>
          ) : preview ? (
            <>
              <strong>{preview.count} matching alerts</strong>
              <span>
                Current snapshot · future matching alerts are also silenced
              </span>
              {preview.alerts.slice(0, 5).map((a) => (
                <code key={a.fingerprint}>
                  {a.labels.namespace || "Cluster-wide"} / {a.labels.alertname}
                </code>
              ))}
            </>
          ) : (
            "Checking matchers…"
          )}
        </div>
        <div className="form-grid">
          <label>
            Starts (local time)
            <input
              type="datetime-local"
              required
              value={start}
              onChange={(e) => {
                setStart(e.target.value);
                setConfirmed(false);
              }}
            />
          </label>
          <label>
            Ends (local time)
            <input
              type="datetime-local"
              required
              value={end}
              onChange={(e) => {
                setEnd(e.target.value);
                setConfirmed(false);
              }}
            />
          </label>
        </div>
        <label>
          Created by
          <input
            required
            value={value.createdBy}
            onChange={(e) => setValue({ ...value, createdBy: e.target.value })}
          />
        </label>
        <label>
          Reason
          <textarea
            required
            rows={2}
            placeholder="Why are these notifications being silenced?"
            value={value.comment}
            onChange={(e) => setValue({ ...value, comment: e.target.value })}
          />
        </label>
        <label className="confirm">
          <input
            type="checkbox"
            checked={confirmed}
            onChange={(e) => setConfirmed(e.target.checked)}
          />
          I reviewed the matchers and duration.
        </label>
        {error && (
          <div role="alert" className="banner danger">
            {error}
          </div>
        )}
        <div className="dialog-actions">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="primary" disabled={busy || !confirmed || !preview}>
            {busy ? "Saving…" : "Save silence"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
