export function WaveDecoration() {
  return (
    <div className="pointer-events-none absolute bottom-0 left-0 right-0 overflow-hidden leading-[0]">
      <svg
        viewBox="0 0 1200 60"
        preserveAspectRatio="none"
        className="block h-[30px] w-full"
        aria-hidden="true"
      >
        <path
          d="M0,30 C200,50 400,10 600,30 C800,50 1000,10 1200,30 L1200,60 L0,60 Z"
          className="fill-[#F0F7FF] dark:fill-gray-950"
        />
      </svg>
    </div>
  )
}
