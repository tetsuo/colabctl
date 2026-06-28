import subprocess

src = r"""
#include <stdio.h>

__global__ void vec_add(float *a, float *b, float *c, int n) {
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i < n) c[i] = a[i] + b[i];
}

int main() {
    const int N = 1 << 20;
    float *ha = new float[N], *hb = new float[N], *hc = new float[N];
    for (int i = 0; i < N; i++) { ha[i] = (float)i; hb[i] = (float)(N - i); }

    float *da, *db, *dc;
    cudaMalloc(&da, N * sizeof(float));
    cudaMalloc(&db, N * sizeof(float));
    cudaMalloc(&dc, N * sizeof(float));
    cudaMemcpy(da, ha, N * sizeof(float), cudaMemcpyHostToDevice);
    cudaMemcpy(db, hb, N * sizeof(float), cudaMemcpyHostToDevice);

    vec_add<<<(N + 255) / 256, 256>>>(da, db, dc, N);
    cudaDeviceSynchronize();

    cudaMemcpy(hc, dc, N * sizeof(float), cudaMemcpyDeviceToHost);
    printf("c[0]      = %.0f\n", hc[0]);
    printf("c[512]    = %.0f\n", hc[512]);
    printf("c[N-1]    = %.0f\n", hc[N - 1]);
    printf("All == N? %s\n", (hc[0] == N && hc[512] == N && hc[N-1] == N) ? "yes" : "no");

    delete[] ha; delete[] hb; delete[] hc;
    cudaFree(da); cudaFree(db); cudaFree(dc);
}
"""

with open('/tmp/vadd.cu', 'w') as f:
    f.write(src)

r = subprocess.run(['nvcc', '-O2', '-o', '/tmp/vadd', '/tmp/vadd.cu'],
                   capture_output=True, text=True)
if r.returncode != 0:
    print("nvcc stderr:", r.stderr)
else:
    print("Compiled OK")
    out = subprocess.run(['/tmp/vadd'], capture_output=True, text=True)
    print(out.stdout)
    if out.stderr:
        print("run stderr:", out.stderr)
