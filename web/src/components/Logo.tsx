// Callme Logo：客服耳麦 + 对话气泡融合图形，颜色跟随当前主题
interface LogoProps {
  size?: number;
  withText?: boolean;
}

export function LogoIcon({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-label="Callme logo">
      <defs>
        <linearGradient id="callme-g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="var(--color-primary)" />
          <stop offset="1" stopColor="var(--color-primary-active)" />
        </linearGradient>
      </defs>
      <path
        d="M32 5 C17.6 5 6.5 15.4 6.5 28.5 c0 7.2 3.3 13.5 8.5 17.8 V55 c0 1.9 2.2 2.9 3.7 1.8 l8.7 -6.6 c1.5 0.2 3 0.3 4.6 0.3 14.4 0 25.5 -10.4 25.5 -23 S46.4 5 32 5 Z"
        fill="url(#callme-g)"
      />
      <path
        d="M19 31 v-2.5 a13 13 0 0 1 26 0 V31"
        fill="none"
        stroke="#fff"
        strokeWidth="3.6"
        strokeLinecap="round"
      />
      <rect x="15.6" y="29.5" width="7" height="11.5" rx="3.5" fill="#fff" />
      <rect x="41.4" y="29.5" width="7" height="11.5" rx="3.5" fill="#fff" />
      <path
        d="M44.9 41 c0 5.4 -5.4 8.2 -10.9 8.2"
        fill="none"
        stroke="#fff"
        strokeWidth="3"
        strokeLinecap="round"
      />
      <circle cx="32.5" cy="49.2" r="2.8" fill="#fff" />
    </svg>
  );
}

export default function Logo({ size = 32, withText = true }: LogoProps) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
      <LogoIcon size={size} />
      {withText && (
        <span
          style={{
            fontSize: size * 0.62,
            fontWeight: 700,
            letterSpacing: 0,
            background: 'linear-gradient(135deg, var(--color-primary), var(--color-primary-active))',
            WebkitBackgroundClip: 'text',
            WebkitTextFillColor: 'transparent',
          }}
        >
          Callme
        </span>
      )}
    </div>
  );
}
