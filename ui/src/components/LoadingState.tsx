interface LoadingStateProps {
  message?: string;
}

export function LoadingState({ message = 'Loading...' }: LoadingStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-12 w-auto animate-pulse opacity-70"
      />
      <p className="text-sm text-[#2a5a7a] dark:text-gray-400">{message}</p>
    </div>
  );
}
