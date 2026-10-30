export function Spinner({ className = 'size-6' }: { className?: string }) {
  return (
    <div
      role="status"
      aria-label="Loading"
      className={`${className} animate-spin rounded-full border-2 border-zinc-300 border-t-indigo-600`}
    />
  )
}
