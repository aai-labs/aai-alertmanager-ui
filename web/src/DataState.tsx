type Props = { label: string; loading?: boolean; detail?: string };
export default function DataState({ label, loading = false, detail }: Props) {
  return (
    <div
      className={`data-state ${loading ? "is-loading" : ""}`}
      role="status"
      aria-live="polite"
      aria-busy={loading}
    >
      <strong>{label}</strong>
      {detail && <p>{detail}</p>}
      {loading && (
        <div className="state-skeleton" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      )}
    </div>
  );
}
