import torch

assert torch.cuda.is_available(), "no CUDA device"
dev = torch.device("cuda")
print("Device:", torch.cuda.get_device_name(0))
print("Torch :", torch.__version__)

# Measured matrix-multiply throughput in TFLOP/s.
n = 4096
a = torch.randn(n, n, device=dev, dtype=torch.float32)
b = torch.randn(n, n, device=dev, dtype=torch.float32)

# Warm up so the timed loop excludes one-time CUDA setup.
for _ in range(3):
    a @ b
torch.cuda.synchronize()

start = torch.cuda.Event(enable_timing=True)
end = torch.cuda.Event(enable_timing=True)

iters = 20
start.record()
for _ in range(iters):
    c = a @ b
end.record()
torch.cuda.synchronize()

ms = start.elapsed_time(end) / iters
flops = 2 * n ** 3  # one multiply and one add per output element
tflops = flops / (ms * 1e-3) / 1e12
print(f"{n}x{n} matmul: {ms:.2f} ms/iter, {tflops:.1f} TFLOP/s (fp32)")
