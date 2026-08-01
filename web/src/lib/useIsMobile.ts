import { useEffect, useState } from 'react'

export function useIsMobile(breakpoint = '(max-width: 767px)') {
  const [matches, setMatches] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia(breakpoint).matches : false,
  )

  useEffect(() => {
    const mq = window.matchMedia(breakpoint)
    const onChange = (e: MediaQueryListEvent) => setMatches(e.matches)
    setMatches(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [breakpoint])

  return matches
}
