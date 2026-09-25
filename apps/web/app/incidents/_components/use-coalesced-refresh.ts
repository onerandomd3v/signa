"use client";

import { useCallback, useEffect, useRef } from "react";

type RefreshOptions<T> = {
  load: (signal: AbortSignal) => Promise<T>;
  onSuccess: (value: T) => void;
  onError: () => void;
  key?: string;
};

/** Serializes authoritative reads and coalesces invalidations while one is in flight. */
export function useCoalescedRefresh<T>({
  load,
  onSuccess,
  onError,
  key,
}: RefreshOptions<T>): () => void {
  const loadRef = useRef(load);
  const successRef = useRef(onSuccess);
  const errorRef = useRef(onError);
  const lifecycleRef = useRef(0);
  const runningRef = useRef(false);
  const pendingRef = useRef(false);
  const controllerRef = useRef<AbortController | null>(null);
  const refreshRef = useRef<() => void>(() => {});

  const refresh = useCallback(() => {
    pendingRef.current = true;
    if (runningRef.current) return;

    runningRef.current = true;
    const lifecycle = lifecycleRef.current;

    const drain = async () => {
      try {
        while (pendingRef.current && lifecycleRef.current === lifecycle) {
          pendingRef.current = false;
          const controller = new AbortController();
          controllerRef.current = controller;

          try {
            const value = await loadRef.current(controller.signal);
            if (
              lifecycleRef.current === lifecycle &&
              !controller.signal.aborted &&
              !pendingRef.current
            ) {
              successRef.current(value);
            }
          } catch {
            if (
              lifecycleRef.current === lifecycle &&
              !controller.signal.aborted &&
              !pendingRef.current
            ) {
              errorRef.current();
            }
          } finally {
            if (controllerRef.current === controller) {
              controllerRef.current = null;
            }
          }
        }
      } finally {
        runningRef.current = false;
        if (pendingRef.current) queueMicrotask(() => refreshRef.current());
      }
    };

    void drain();
  }, []);

  useEffect(() => {
    loadRef.current = load;
    successRef.current = onSuccess;
    errorRef.current = onError;
    refreshRef.current = refresh;
  }, [load, onError, onSuccess, refresh]);

  useEffect(() => {
    lifecycleRef.current += 1;
    refresh();

    return () => {
      lifecycleRef.current += 1;
      pendingRef.current = false;
      controllerRef.current?.abort();
    };
  }, [key, refresh]);

  return refresh;
}
