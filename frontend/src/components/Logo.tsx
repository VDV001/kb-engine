/**
 * The DVK mark, carried over from the Python dashboard so the two surfaces
 * read as one product rather than two tools that happen to share a palette.
 *
 * D is the half-round, V the triangle, K the two rays. All three are the
 * secondary colour at descending opacity, which is what keeps a three-part
 * mark from looking like three logos.
 */
export function Logo({ className = '' }: { className?: string }) {
  return (
    <svg
      width="42"
      height="36"
      viewBox="0 0 42 36"
      fill="none"
      role="img"
      aria-label="kb-engine"
      className={className}
    >
      <path d="M4,4 L4,32 Q20,32 20,18 Q20,4 4,4 Z" fill="var(--secondary)" opacity="0.9" />
      <polygon points="16,5 25,32 34,5" fill="var(--secondary)" opacity="0.55" />
      <line
        x1="28" y1="18" x2="38" y2="5"
        stroke="var(--secondary)" strokeWidth="2.8" strokeLinecap="round" opacity="0.35"
      />
      <line
        x1="28" y1="18" x2="38" y2="31"
        stroke="var(--secondary)" strokeWidth="2.8" strokeLinecap="round" opacity="0.35"
      />
    </svg>
  )
}
