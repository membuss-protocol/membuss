---
id: counting-bloom-filter
title: O(1) Counting Bloom Filter Engine Specification
sidebar_label: Counting Bloom Filter Engine
---

# O(1) Counting Bloom Filter Engine Specification

Membuss implements a thread-safe **Counting Bloom Filter** (`core/store/counting_bloom.go`) for instant block existence verification.

---

## 1. Mathematical Formulas & Double Hashing

- **Bucket Array (`m`)**: Array of 8-bit saturating counters (`[]uint8`).
- **Optimal Buckets Formula**:
  `m = - (n * ln(p)) / (ln(2)^2)`
- **Optimal Hash Functions Formula**:
  `k = (m / n) * ln(2)`
- **Kirsch-Mitzenmacher Double Hashing**:
  `h_i(x) = (h_1(x) + i * h_2(x)) mod m`
  where `h_1(x)` is FNV-1a 64-bit hash and `h_2(x)` is derived via bit rotation.

---

## 2. Operations & Counter Saturation

- **`Add(data)`**: Increments `k` bucket counters (`O(1)`). Counters saturate at 255 to prevent overflow wrapping.
- **`Remove(data)`**: Decrements `k` bucket counters (`O(1)`). Counter floors at 0.
- **`Test(data)`**: Returns `false` if any of the `k` bucket counters is 0 (`O(1)`).

This eliminates the need for background database rebuild workers during high-rate deletions.
