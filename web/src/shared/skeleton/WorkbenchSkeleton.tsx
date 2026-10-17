/** The workbench's shape while the session loads: title, stage and panel. */
export function WorkbenchSkeleton() {
  return (
    <div className="ws" aria-busy="true" aria-label="작업을 불러오는 중">
      <div className="ws-head">
        <div className="ws-heading skeleton-page">
          <span className="skeleton skeleton-heading" aria-hidden="true" />
          <span className="skeleton skeleton-line" aria-hidden="true" />
        </div>
      </div>
      <div className="ws-stage">
        <span className="skeleton skeleton-stage" aria-hidden="true" />
      </div>
      <div className="ws-panel">
        <span className="skeleton skeleton-block" aria-hidden="true" />
      </div>
    </div>
  );
}
