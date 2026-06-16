// Agent 代表图标：信使之翼意象，用于对话中的助手头像
export default function HermesIcon({ size = 20, color = '#fff' }: { size?: number; color?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-label="Callme assistant">
      {/* 三层羽翼，向右上扬起 */}
      <path
        d="M3 17 C7 17 10.5 15.8 13.5 13.5 C16.5 11.2 18.8 8.2 20.5 4.5 C17 6 14.5 6.5 11.5 6.5 C13 7.5 13.8 8.2 14.5 9.5 C11.8 9.2 9.8 9.5 7.5 10.8 C9.2 11.4 10 12 11 13.2 C8.2 13.6 5.8 14.8 3 17 Z"
        fill={color}
      />
      {/* 翼根圆点（信使杖头） */}
      <circle cx="4.6" cy="19.4" r="1.8" fill={color} />
    </svg>
  );
}
