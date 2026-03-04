import http from 'k6/http';
import { sleep, check } from 'k6';

export const options = {
    scenarios: {
        constant_load: {
            executor: 'constant-arrival-rate',
            rate: 50000, // 50k request/s
            timeUnit: '1s',
            duration: '30s',
            preAllocatedVUs: 200,
            maxVUs: 1000,
        },
    },
};

export default function () {
    // 1. Gọi API của Kitwork
    const res = http.get('http://localhost:8080/hello');

    // 2. Kiểm tra xem server có trả về 200 hay không
    check(res, {
        'status is 200': (r) => r.status === 200,
        'body is hello': (r) => r.body === 'hello world',
    });

    // Nghỉ một chút giữa các request (tùy chọn)
    // sleep(1); 
}
