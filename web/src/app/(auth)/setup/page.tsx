"use client";

import { useEffect, useState } from "react";

import { useRouter } from "next/navigation";

import { ArrowRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

const STARS = [
  { cx: 88, cy: 72, r: 1.4, o: 0.7 },
  { cx: 195, cy: 162, r: 1.0, o: 0.45 },
  { cx: 310, cy: 64, r: 1.5, o: 0.75 },
  { cx: 740, cy: 82, r: 1.3, o: 0.65 },
  { cx: 885, cy: 145, r: 1.1, o: 0.5 },
  { cx: 1035, cy: 68, r: 1.6, o: 0.75 },
  { cx: 1185, cy: 138, r: 1.1, o: 0.45 },
  { cx: 1360, cy: 84, r: 1.5, o: 0.7 },
  { cx: 1520, cy: 152, r: 1.0, o: 0.4 },
  { cx: 1675, cy: 76, r: 1.6, o: 0.75 },
  { cx: 1825, cy: 135, r: 1.2, o: 0.5 },
  { cx: 72, cy: 295, r: 1.1, o: 0.45 },
  { cx: 168, cy: 420, r: 1.0, o: 0.35 },
  { cx: 690, cy: 290, r: 1.2, o: 0.5 },
  { cx: 840, cy: 360, r: 1.0, o: 0.35 },
  { cx: 1010, cy: 265, r: 1.3, o: 0.5 },
  { cx: 1290, cy: 255, r: 1.1, o: 0.4 },
  { cx: 1790, cy: 285, r: 1.3, o: 0.5 },
  { cx: 235, cy: 238, r: 2.1, o: 0.85 },
  { cx: 276, cy: 274, r: 1.8, o: 0.7 },
  { cx: 328, cy: 248, r: 1.9, o: 0.75 },
  { cx: 292, cy: 214, r: 1.8, o: 0.7 },
  { cx: 368, cy: 190, r: 2.0, o: 0.8 },
  { cx: 442, cy: 162, r: 2.1, o: 0.85 },
] as const;

export default function SetupPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    api
      .authStatus()
      .then(({ initialized }) => {
        if (initialized) router.replace("/login");
      })
      .catch(() => setError("无法连接到后端服务"))
      .finally(() => setChecking(false));
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setError("两次输入的密码不一致");
      return;
    }
    if (password.length < 8) {
      setError("密码长度至少 8 位");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.initPassword(password);
      auth.setToken(token);
      router.replace("/function/tasks");
    } catch (err) {
      setError(err instanceof Error ? err.message : "初始化失败");
    } finally {
      setLoading(false);
    }
  }

  if (checking) return null;

  return (
    <div className="relative flex min-h-dvh w-full flex-col justify-between overflow-hidden bg-[#04060e] px-6 py-6 select-none sm:px-10 lg:px-16">
      <svg
        viewBox="0 0 1920 1080"
        preserveAspectRatio="xMidYMid slice"
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 h-full w-full"
      >
        <defs>
          <linearGradient id="skyGrad" x1="0%" y1="0%" x2="0%" y2="100%">
            <stop offset="0%" stopColor="#03050c" />
            <stop offset="50%" stopColor="#080d20" />
            <stop offset="78%" stopColor="#121a3a" />
            <stop offset="100%" stopColor="#1b1947" />
          </linearGradient>
          <radialGradient id="polarisAura" cx="27.1%" cy="13.1%" r="38%">
            <stop offset="0%" stopColor="#e0e7ff" stopOpacity="0.55" />
            <stop offset="18%" stopColor="#818cf8" stopOpacity="0.22" />
            <stop offset="50%" stopColor="#4f46e5" stopOpacity="0.06" />
            <stop offset="100%" stopColor="#04060e" stopOpacity="0" />
          </radialGradient>
          <linearGradient id="guidingBeam" x1="27%" y1="13%" x2="40%" y2="78%">
            <stop offset="0%" stopColor="#e0e7ff" stopOpacity="0.42" />
            <stop offset="32%" stopColor="#818cf8" stopOpacity="0.14" />
            <stop offset="75%" stopColor="#6366f1" stopOpacity="0.04" />
            <stop offset="100%" stopColor="#6366f1" stopOpacity="0" />
          </linearGradient>
          <radialGradient id="passGlow" cx="28%" cy="68%" r="36%">
            <stop offset="0%" stopColor="#818cf8" stopOpacity="0.36" />
            <stop offset="45%" stopColor="#4f46e5" stopOpacity="0.11" />
            <stop offset="100%" stopColor="#04060e" stopOpacity="0" />
          </radialGradient>
          <linearGradient id="trailFill" x1="0%" y1="0%" x2="0%" y2="100%">
            <stop offset="0%" stopColor="#e0e7ff" stopOpacity="0.65" />
            <stop offset="35%" stopColor="#a5b4fc" stopOpacity="0.32" />
            <stop offset="72%" stopColor="#6366f1" stopOpacity="0.16" />
            <stop offset="100%" stopColor="#312e81" stopOpacity="0.05" />
          </linearGradient>
        </defs>

        <rect width="1920" height="1080" fill="url(#skyGrad)" />
        <rect width="1920" height="1080" fill="url(#polarisAura)" />
        <rect width="1920" height="1080" fill="url(#passGlow)" />

        <circle
          cx="520"
          cy="142"
          r="175"
          fill="none"
          stroke="#a5b4fc"
          strokeOpacity="0.1"
          strokeWidth="1"
          strokeDasharray="6 14"
        />
        <circle
          cx="520"
          cy="142"
          r="325"
          fill="none"
          stroke="#818cf8"
          strokeOpacity="0.07"
          strokeWidth="1"
          strokeDasharray="12 22"
        />
        <circle cx="520" cy="142" r="495" fill="none" stroke="#6366f1" strokeOpacity="0.045" strokeWidth="1" />

        <polyline
          points="235,238 276,274 328,248 292,214 235,238"
          fill="none"
          stroke="#818cf8"
          strokeOpacity="0.28"
          strokeWidth="1.1"
        />
        <polyline
          points="292,214 368,190 442,162 520,142"
          fill="none"
          stroke="#c7d2fe"
          strokeOpacity="0.38"
          strokeWidth="1.2"
          strokeDasharray="3 4"
        />

        {STARS.map((s, idx) => (
          <circle key={idx} cx={s.cx} cy={s.cy} r={s.r} fill="#e0e7ff" fillOpacity={s.o} />
        ))}

        <polygon points="520,142 360,760 880,820" fill="url(#guidingBeam)" />
        <line
          x1="520"
          y1="142"
          x2="768"
          y2="766"
          stroke="#e0e7ff"
          strokeOpacity="0.28"
          strokeWidth="1.1"
          strokeDasharray="4 8"
        />

        <g transform="translate(520, 142)">
          <circle r="48" fill="#818cf8" fillOpacity="0.2" />
          <circle r="20" fill="#c7d2fe" fillOpacity="0.38" />
          <path
            d="M 0,-72 L 4.2,-8 L 72,0 L 4.2,8 L 0,72 L -4.2,8 L -72,0 L -4.2,-8 Z"
            fill="#ffffff"
            fillOpacity="0.96"
          />
          <path
            d="M -28,-28 L -2.8,-4.5 L 28,-28 L 4.5,-2.8 L 28,28 L 2.8,4.5 L -28,28 L -4.5,2.8 Z"
            fill="#a5b4fc"
            fillOpacity="0.82"
          />
          <circle r="5.5" fill="#ffffff" />
        </g>

        <path
          d="M 0,790 L 165,710 L 355,755 L 465,695 L 520,722 L 585,688 L 790,750 L 1030,695 L 1290,765 L 1560,715 L 1770,760 L 1920,735 L 1920,1080 L 0,1080 Z"
          fill="#0c1229"
        />
        <path
          d="M 0,845 L 220,785 L 440,810 L 635,768 L 860,805 L 1140,775 L 1430,830 L 1710,795 L 1920,835 L 1920,1080 L 0,1080 Z"
          fill="#080c1d"
        />
        <path
          d="M 0,900 Q 260,855 520,885 T 1060,915 Q 1480,880 1920,930 L 1920,1080 L 0,1080 Z"
          fill="#04060e"
        />

        <path
          d="M 520,722
             C 425,758 415,792 545,812
             C 695,834 785,822 752,858
             C 712,898 520,918 558,965
             C 595,1012 790,1042 860,1080
             L 1035,1080
             C 915,1032 665,1000 622,956
             C 582,914 778,895 802,850
             C 824,808 712,820 562,800
             C 442,782 445,754 526,722 Z"
          fill="url(#trailFill)"
        />
        <path
          d="M 520,722
             C 425,758 415,792 545,812
             C 695,834 785,822 752,858
             C 712,898 520,918 558,965
             C 595,1012 790,1042 860,1080"
          fill="none"
          stroke="#c7d2fe"
          strokeOpacity="0.5"
          strokeWidth="1.6"
        />
        <path
          d="M 526,722
             C 445,754 442,782 562,800
             C 712,820 824,808 802,850
             C 778,895 582,914 622,956
             C 665,1000 915,1032 1035,1080"
          fill="none"
          stroke="#818cf8"
          strokeOpacity="0.32"
          strokeWidth="1.2"
        />

        <g transform="translate(768, 818)">
          <ellipse cx="0" cy="26" rx="26" ry="4.5" fill="#a5b4fc" fillOpacity="0.42" />
          <circle cx="-2" cy="-22" r="5.2" fill="#070a18" stroke="#c7d2fe" strokeOpacity="0.8" strokeWidth="1.2" />
          <path
            d="M -6,-15 L 5,-15 L 14,10 L 10,26 L -9,26 L -11,8 Z"
            fill="#060916"
            stroke="#a5b4fc"
            strokeOpacity="0.6"
            strokeWidth="1.2"
          />
          <line x1="-5" y1="-12" x2="-15" y2="-21" stroke="#c7d2fe" strokeOpacity="0.75" strokeWidth="2" />
          <line x1="-16" y1="-30" x2="-11" y2="26" stroke="#c7d2fe" strokeOpacity="0.8" strokeWidth="1.5" />
          <circle cx="-16" cy="-31" r="6.5" fill="#818cf8" fillOpacity="0.4" />
          <circle cx="-16" cy="-31" r="2.8" fill="#ffffff" />
        </g>
      </svg>

      <div className="relative z-10 flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src="/logo.png"
            alt="Lodestar"
            width={28}
            height={28}
            className="drop-shadow-[0_0_12px_rgba(129,140,248,0.65)]"
          />
          <span className="font-mono text-indigo-100/80 text-xs tracking-[0.28em]">LODESTAR</span>
        </div>
        <span className="hidden font-mono text-[11px] text-indigo-200/45 tracking-widest sm:inline">
          α URSAE MINORIS · N 90°00′
        </span>
      </div>

      <div className="relative z-10 my-auto flex w-full justify-center lg:justify-end lg:pr-[4vw] xl:pr-[7vw]">
        <div className="w-full max-w-[clamp(340px,26vw,390px)] rounded-2xl border border-indigo-200/[0.14] bg-[#0a0f22]/65 p-7 shadow-[0_24px_64px_-12px_rgba(0,0,0,0.82),0_0_36px_-8px_rgba(99,102,241,0.22)] backdrop-blur-md sm:p-8">
          <div className="flex flex-col">
            <h1 className="font-semibold text-xl tracking-[0.18em] text-white">初始化密码</h1>
            <p className="mt-1.5 text-indigo-200/65 text-xs">首次使用 Lodestar，请设置至少 8 位登录密码</p>
          </div>

          <form onSubmit={handleSubmit} className="mt-6 flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="password" className="font-medium text-indigo-100/75 text-xs">
                新密码
              </Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="至少 8 位"
                autoFocus
                autoComplete="new-password"
                className="h-10 border-white/12 bg-white/[0.06] text-sm text-white placeholder:text-slate-400/60 focus-visible:border-indigo-400 focus-visible:ring-indigo-500/30"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="confirm" className="font-medium text-indigo-100/75 text-xs">
                确认密码
              </Label>
              <Input
                id="confirm"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder="再次输入密码"
                autoComplete="new-password"
                className="h-10 border-white/12 bg-white/[0.06] text-sm text-white placeholder:text-slate-400/60 focus-visible:border-indigo-400 focus-visible:ring-indigo-500/30"
              />
            </div>

            {error && (
              <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-center text-red-300 text-xs">
                {error}
              </div>
            )}

            <Button
              type="submit"
              disabled={loading || !password || !confirm}
              className="mt-1 h-10 w-full gap-2 bg-linear-to-r from-indigo-500/90 to-violet-600/90 font-medium text-sm text-white shadow-[0_4px_24px_-2px_rgba(99,102,241,0.55)] transition-all hover:from-indigo-500 hover:to-violet-500 disabled:opacity-40"
            >
              <span>{loading ? "保存中..." : "点亮北极星并启程"}</span>
              {!loading && <ArrowRight className="size-4" />}
            </Button>
          </form>
        </div>
      </div>

      <div className="relative z-10 flex items-center justify-between font-mono text-[11px] text-indigo-200/35 tracking-widest">
        <span>POLARIS AUTONOMOUS ENGINE</span>
        <span className="sm:hidden">N 90°00′</span>
      </div>
    </div>
  );
}
