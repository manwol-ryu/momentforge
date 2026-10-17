/** The app's mark: a few density bars and a candidate marker, as on the timeline (and the favicon). */
export function BrandMark() {
  return (
    <svg className="brand-mark" viewBox="0 0 18 18" aria-hidden="true">
      <rect className="mark-bar" x="1" y="9" width="2" height="4" />
      <rect className="mark-bar" x="4.5" y="5" width="2" height="8" />
      <rect className="mark-bar" x="8" y="7" width="2" height="6" />
      <rect className="mark-bar" x="11.5" y="2" width="2" height="11" />
      <rect className="mark-bar" x="15" y="8" width="2" height="5" />
      <path className="mark-pin" d="M12.5 14.5l2.2 3h-4.4z" />
    </svg>
  );
}
