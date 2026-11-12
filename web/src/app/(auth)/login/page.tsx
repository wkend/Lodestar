"use client";

import { useEffect, useState } from "react";

import { useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function LoginPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    // 已登录直接进主界面（静态导出下无 middleware 代劳这层跳转）。
    if (auth.getToken()) {
      router.replace("/function/tasks");
      return;
    }
    api.authStatus()
      .then(({ initialized }) => {
        if (!initialized) router.replace("/setup");
      })
      .catch(() => setError("无法连接到后端服务"))
      .finally(() => setChecking(false));
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    setError("");
    try {
      const { token } = await api.login("Lodestar", password);
      auth.setToken(token);
      router.replace("/function/tasks");
    } catch {
      setError("用户名或密码错误");
    } finally {
      setLoading(false);
    }
  }

  if (checking) return null;

  return (
    <div className="flex h-dvh">
      {/* Left panel: Lodestar celestial navigation theme */}
      <div
        className="relative hidden flex-col items-center justify-between overflow-hidden p-12 text-center text-white lg:flex lg:w-2/5"
        style={{
          background:
            "radial-gradient(circle at 50% 38%, rgba(99, 102, 241, 0.32) 0%, rgba(79, 70, 229, 0.14) 38%, transparent 70%), linear-gradient(160deg, #0b0e1e 0%, #151934 52%, #1d1842 100%)",
        }}
      >
        {/* Starfield & celestial grid */}
        <div
          className="pointer-events-none absolute inset-0 opacity-25"
          style={{
            backgroundImage:
              "radial-gradient(rgba(255,255,255,0.75) 1px, transparent 1px), radial-gradient(rgba(165,180,252,0.5) 1px, transparent 1px)",
            backgroundSize: "48px 48px, 96px 96px",
            backgroundPosition: "0 0, 24px 24px",
          }}
        />
        <div className="relative z-10 flex items-center gap-2 self-start text-indigo-200/80 text-xs tracking-widest uppercase">
          <span className="inline-block size-2 rounded-full bg-indigo-400 shadow-[0_0_8px_#818cf8]" />
          Celestial Navigation · Multi-Brain Security Engine
        </div>

        {/* Astrolabe compass & Polaris centerpiece */}
        <div className="relative z-10 flex items-center justify-center my-auto">
          <div className="absolute size-96 rounded-full border border-indigo-400/10" />
          <div className="absolute size-72 rounded-full border border-dashed border-indigo-300/20" />
          <div className="absolute size-52 rounded-full border border-indigo-300/25 bg-indigo-950/30 backdrop-blur-xs" />
          <div className="absolute h-80 w-px bg-linear-to-b from-transparent via-indigo-400/25 to-transparent" />
          <div className="absolute h-px w-80 bg-linear-to-r from-transparent via-indigo-400/25 to-transparent" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src="/logo.png"
            alt="Lodestar"
            width={156}
            height={156}
            className="relative drop-shadow-[0_0_28px_rgba(129,140,248,0.55)]"
          />
        </div>

        <div className="relative z-10 max-w-sm space-y-2">
          <div className="font-semibold text-lg tracking-wide text-white">LODESTAR</div>
          <p className="text-indigo-200/70 text-xs leading-relaxed">
            以可追溯推理图为星图 · 多脑协同规划、执行、研判与独立复核
          </p>
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-3/5">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">登录</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">欢迎回来，请输入密码以继续使用 Lodestar</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">用户名</Label>
              <Input id="username" value="Lodestar" readOnly className="bg-muted text-muted-foreground" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">密码</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="请输入密码"
                autoFocus
                autoComplete="current-password"
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading || !password}>
              {loading ? "登录中..." : "登录"}
            </Button>
          </form>
        </div>
      </div>
    </div>
  );
}
