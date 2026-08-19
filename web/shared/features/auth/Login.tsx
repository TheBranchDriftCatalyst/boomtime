import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { Link, useNavigate } from "react-router";
import { z } from "zod";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { useAuth } from "@shared/features/auth/useAuth";
import { usePublicConfig } from "@shared/lib/usePublicConfig";
import { ApiError } from "@shared/lib/api";

const schema = z.object({
  username: z.string().min(1, "Username is required"),
  password: z.string().min(1, "Password is required"),
});
type FormValues = z.infer<typeof schema>;

export function Login() {
  const { login, isLoggedIn } = useAuth();
  const { config } = usePublicConfig();
  const navigate = useNavigate();
  const [error, setError] = useState("");

  // gaka-93f.11.4: when the server runs Authentik-only (auth_provider=oidc),
  // password sign-in is disabled server-side — offer ONLY "Continue with
  // Authentik". When local but OIDC is configured, offer both. The button is a
  // full-page navigation (not fetch) so the browser follows the 302 to the
  // provider and back through /auth/callback/oidc.
  const oidcOnly = config.auth_provider === "oidc";
  const oidcAvailable = config.oidc_enabled;
  const startOIDC = () => {
    window.location.href = "/auth/login/oidc";
  };

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>();

  useEffect(() => {
    if (isLoggedIn) navigate("/app", { replace: true });
  }, [isLoggedIn, navigate]);

  async function onSubmit(values: FormValues) {
    const parsed = schema.safeParse(values);
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Invalid input");
      return;
    }
    try {
      await login(parsed.data);
      setError("");
      navigate("/app");
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : "Unknown error";
      setError(`Login failed: ${msg}`);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 p-4">
      <Card className="w-full max-w-sm">
        <CardContent className="pt-6">
          <div className="mb-6 flex flex-col items-center gap-2">
            <img
              src="/boomtime.svg"
              alt="Boomtime"
              className="h-11 w-11 rounded-xl"
            />
            <h1 className="text-xl font-semibold">Welcome back</h1>
            <p className="text-sm text-muted-foreground">
              Sign in to your Boomtime account
            </p>
          </div>
          {oidcOnly ? (
            <div className="space-y-4">
              <Button type="button" className="w-full" onClick={startOIDC}>
                Continue with Authentik
              </Button>
              <p className="text-center text-xs text-muted-foreground">
                This server uses Authentik single sign-on.
              </p>
            </div>
          ) : (
            <>
              <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
                <div className="space-y-1.5">
                  <Label htmlFor="username">Username</Label>
                  <Input id="username" autoFocus {...register("username")} />
                  {errors.username && (
                    <p className="text-xs text-destructive">
                      {errors.username.message}
                    </p>
                  )}
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="password">Password</Label>
                  <Input
                    id="password"
                    type="password"
                    {...register("password")}
                  />
                  {errors.password && (
                    <p className="text-xs text-destructive">
                      {errors.password.message}
                    </p>
                  )}
                </div>
                <Button type="submit" className="w-full" disabled={isSubmitting}>
                  {isSubmitting ? "Signing in..." : "Sign in"}
                </Button>
                {error && (
                  <p className="text-center text-sm text-destructive">{error}</p>
                )}
              </form>
              {oidcAvailable && (
                <>
                  <div className="my-4 flex items-center gap-3">
                    <div className="h-px flex-1 bg-border" />
                    <span className="text-xs text-muted-foreground">or</span>
                    <div className="h-px flex-1 bg-border" />
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    className="w-full"
                    onClick={startOIDC}
                  >
                    Continue with Authentik
                  </Button>
                </>
              )}
              <p className="mt-6 text-center text-sm text-muted-foreground">
                Don&apos;t have an account?{" "}
                <Link to="/register" className="font-medium text-primary hover:underline">
                  Register here
                </Link>
              </p>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
