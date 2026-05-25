import http from 'k6/http';
import { sleep, check } from 'k6';

export const options = {
    scenarios: {
        constant_load: {
            executor: 'constant-arrival-rate',
            rate: 15000, // 15k request/s
            timeUnit: '1s',
            duration: '10s',
            preAllocatedVUs: 50,
            maxVUs: 200,
        },
    },
};

export default function () {
    // 1. Gọi API của Kitwork
    const res = http.get('http://localhost:8080/teststatic');

    // 2. Kiểm tra xem server có trả về 200 hay không
    check(res, {
        'status is 200': (r) => r.status === 200,
        'body is static': (r) => r.body === 'static cache output',
    });

    // Nghỉ một chút giữa các request (tùy chọn)
    // sleep(1); 
}
